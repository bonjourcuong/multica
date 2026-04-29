// Documents handler — read-only (MUL-16) + write/create/delete (MUL-18)
// on .md files in a workspace's PKM folder.
//
// Read-only endpoints use the documents.Resolver (initialised once at
// startup from MULTICA_PKM_ROOT and stored on Handler.Documents).
//
// Write endpoints open a pkmfs.FS per request so each handler gets its
// own sandboxed root and can close it promptly.

package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"sort"
	"strings"

	"github.com/multica-ai/multica/server/internal/documents"
	"github.com/multica-ai/multica/server/internal/logger"
	"github.com/multica-ai/multica/server/internal/pkmfs"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// ---------------------------------------------------------------------------
// Shared helpers
// ---------------------------------------------------------------------------

// pkmPathSettingKey is the key used inside the workspace `settings` JSON
// column to hold the workspace's PKM root (a path RELATIVE to the server
// allowlist root). MUL-14 will eventually move this into a dedicated column;
// reading from settings here keeps PR3 unblocked and makes that migration a
// localized change to workspacePKMPath().
const pkmPathSettingKey = "pkm_path"

// pkmContextFilesSettingKey holds the list of PKM-relative paths that are
// auto-injected into every agent's context (CLAUDE.md / AGENTS.md) at task
// dispatch. Stored under workspace.settings as a JSON array of strings.
const pkmContextFilesSettingKey = "pkm_context_files"

// workspacePKMContextFiles returns the list of PKM-relative paths configured
// for auto-injection into agent context. Returns nil when not configured or
// when the value is malformed (we silently skip rather than fail-open).
func workspacePKMContextFiles(ws db.Workspace) []string {
	if len(ws.Settings) == 0 {
		return nil
	}
	var settings map[string]any
	if err := json.Unmarshal(ws.Settings, &settings); err != nil {
		return nil
	}
	raw, ok := settings[pkmContextFilesSettingKey]
	if !ok {
		return nil
	}
	arr, ok := raw.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, v := range arr {
		s, ok := v.(string)
		if !ok {
			continue
		}
		s = strings.TrimSpace(s)
		s = strings.TrimPrefix(s, "/")
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

// workspacePKMPath extracts the configured pkm_path for a workspace from its
// settings JSON. Returns "" when not configured.
//
// The on-disk canonical form starts with "/" (validated by pkmpath.Normalize
// in the settings handler), but documents.Resolve expects a path RELATIVE
// to MULTICA_PKM_ROOT — i.e. without a leading "/". We strip it here so the
// read API matches the write API's behavior in workspacePKMPathFromSettings.
func workspacePKMPath(ws db.Workspace) string {
	if len(ws.Settings) == 0 {
		return ""
	}
	var settings map[string]any
	if err := json.Unmarshal(ws.Settings, &settings); err != nil {
		return ""
	}
	raw, ok := settings[pkmPathSettingKey]
	if !ok {
		return ""
	}
	s, ok := raw.(string)
	if !ok {
		return ""
	}
	return strings.TrimPrefix(strings.TrimSpace(s), "/")
}

// pkmContextFileMaxBytes caps each PKM context file so a single oversized
// note can't blow up every claim response. 256 KiB is plenty for an
// architecture doc and keeps the agent's effective system prompt bounded.
const pkmContextFileMaxBytes = 256 << 10

// buildWorkspaceContextBlock renders the section appended to agent
// instructions at task claim. It concatenates:
//   - workspace.context (free-form text from settings)
//   - the contents of each file listed in workspace.settings.pkm_context_files
//
// Returns "" when nothing is configured. Files that fail to read are
// silently skipped — they're context, not correctness, so a missing or
// renamed doc must not break agent dispatch.
func (h *Handler) buildWorkspaceContextBlock(ctx context.Context, ws db.Workspace) string {
	freeForm := ""
	if ws.Context.Valid {
		freeForm = strings.TrimSpace(ws.Context.String)
	}

	pkmFiles := workspacePKMContextFiles(ws)
	pkmPath := workspacePKMPath(ws)

	type loadedFile struct {
		path    string
		content string
	}
	var loaded []loadedFile
	if len(pkmFiles) > 0 && pkmPath != "" && h.Documents != nil {
		for _, rel := range pkmFiles {
			abs, err := h.Documents.Resolve(pkmPath, rel)
			if err != nil {
				slog.Warn("pkm context file: resolve failed", "workspace_id", uuidToString(ws.ID), "path", rel, "error", err)
				continue
			}
			data, _, err := documents.ReadMarkdown(abs, pkmContextFileMaxBytes)
			if err != nil {
				slog.Warn("pkm context file: read failed", "workspace_id", uuidToString(ws.ID), "path", rel, "error", err)
				continue
			}
			loaded = append(loaded, loadedFile{path: rel, content: string(data)})
		}
	}

	if freeForm == "" && len(loaded) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("## Workspace Context\n\n")
	if freeForm != "" {
		b.WriteString(freeForm)
		b.WriteString("\n\n")
	}
	for _, f := range loaded {
		b.WriteString("### From PKM: `")
		b.WriteString(f.path)
		b.WriteString("`\n\n")
		b.WriteString(strings.TrimSpace(f.content))
		b.WriteString("\n\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// ---------------------------------------------------------------------------
// Read-only helpers & handlers (MUL-16)
// ---------------------------------------------------------------------------

// requireDocumentsContext validates membership, loads the workspace, and
// returns the resolved on-disk path for the ?path= query parameter. It
// writes the appropriate HTTP error itself when anything goes wrong, in
// which case the second return is false.
func (h *Handler) requireDocumentsContext(w http.ResponseWriter, r *http.Request) (string, bool) {
	if h.Documents == nil {
		writeError(w, http.StatusServiceUnavailable, "documents api not configured")
		return "", false
	}

	workspaceID := workspaceIDFromURL(r, "id")
	if _, ok := h.workspaceMember(w, r, workspaceID); !ok {
		return "", false
	}

	ws, err := h.Queries.GetWorkspace(r.Context(), parseUUID(workspaceID))
	if err != nil {
		writeError(w, http.StatusNotFound, "workspace not found")
		return "", false
	}
	pkm := workspacePKMPath(ws)
	if pkm == "" {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "pkm_path not configured for this workspace",
			"code":  "pkm_not_configured",
		})
		return "", false
	}

	rel := r.URL.Query().Get("path")
	abs, err := h.Documents.Resolve(pkm, rel)
	if err != nil {
		mapDocumentsError(w, err)
		return "", false
	}
	return abs, true
}

// mapDocumentsError translates a documents package error into an HTTP
// response. Keeps handler bodies free of the same six branches each.
func mapDocumentsError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, documents.ErrNotConfigured):
		writeError(w, http.StatusServiceUnavailable, "documents api not configured")
	case errors.Is(err, documents.ErrInvalidPath):
		writeError(w, http.StatusBadRequest, "invalid path")
	case errors.Is(err, documents.ErrOutsideRoot), errors.Is(err, documents.ErrSymlinkEscape):
		writeError(w, http.StatusForbidden, "path outside workspace")
	case errors.Is(err, documents.ErrExtNotAllowed):
		writeError(w, http.StatusUnsupportedMediaType, "file type not allowed")
	case errors.Is(err, documents.ErrNotFound):
		writeError(w, http.StatusNotFound, "not found")
	case errors.Is(err, documents.ErrNotRegular):
		writeError(w, http.StatusBadRequest, "not a regular file")
	case errors.Is(err, documents.ErrNotDirectory):
		writeError(w, http.StatusBadRequest, "not a directory")
	case errors.Is(err, documents.ErrTooLarge):
		writeError(w, http.StatusRequestEntityTooLarge, "file exceeds size cap")
	case errors.Is(err, os.ErrNotExist):
		writeError(w, http.StatusNotFound, "not found")
	case errors.Is(err, os.ErrPermission):
		writeError(w, http.StatusForbidden, "permission denied")
	default:
		writeError(w, http.StatusInternalServerError, "internal error")
	}
}

// DocumentTreeResponse is what GET /documents/tree returns.
type DocumentTreeResponse struct {
	Path    string            `json:"path"`
	Entries []documents.Entry `json:"entries"`
}

// GetDocumentsTree handles GET /api/workspaces/{id}/documents/tree?path=<rel>.
// Returns the entries of one directory (non-recursive), sorted with
// directories first then by name.
func (h *Handler) GetDocumentsTree(w http.ResponseWriter, r *http.Request) {
	abs, ok := h.requireDocumentsContext(w, r)
	if !ok {
		return
	}
	entries, err := h.Documents.ListDir(abs)
	if err != nil {
		mapDocumentsError(w, err)
		return
	}

	rel := strings.TrimSpace(r.URL.Query().Get("path"))

	// Populate the path field on each entry (parent + "/" + name).
	for i := range entries {
		if rel == "" || rel == "." {
			entries[i].Path = entries[i].Name
		} else {
			entries[i].Path = rel + "/" + entries[i].Name
		}
	}

	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].Type != entries[j].Type {
			return entries[i].Type == "folder"
		}
		return entries[i].Name < entries[j].Name
	})

	writeJSON(w, http.StatusOK, DocumentTreeResponse{Path: rel, Entries: entries})
}

// DocumentFileResponse is what GET /documents/file returns. We send the body
// as a JSON string instead of raw text/markdown so the response carries
// metadata (path, mtime, size) alongside content. The frontend already deals
// in JSON for everything else; mixing content types would force a special
// case in the API client.
type DocumentFileResponse struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	Size    int    `json:"size"`
	ModTime string `json:"mtime"`
}

// GetDocumentsFile handles GET /api/workspaces/{id}/documents/file?path=<rel>.
// Reads a `.md` file (the only extension allowed) up to the configured size
// cap and returns its content as JSON.
func (h *Handler) GetDocumentsFile(w http.ResponseWriter, r *http.Request) {
	abs, ok := h.requireDocumentsContext(w, r)
	if !ok {
		return
	}
	data, mtime, err := documents.ReadMarkdown(abs, documents.DefaultMarkdownMaxBytes)
	if err != nil {
		mapDocumentsError(w, err)
		return
	}
	rel := strings.TrimSpace(r.URL.Query().Get("path"))
	writeJSON(w, http.StatusOK, DocumentFileResponse{
		Path:    rel,
		Content: string(data),
		Size:    len(data),
		ModTime: mtime.Format("2006-01-02T15:04:05Z07:00"),
	})
}

// GetDocumentsImage handles GET /api/workspaces/{id}/documents/image?path=<rel>.
// Streams an image (allowlisted extension only) with explicit Content-Type
// and X-Content-Type-Options: nosniff. Caller-supplied filename never reaches
// a Content-Disposition header.
func (h *Handler) GetDocumentsImage(w http.ResponseWriter, r *http.Request) {
	abs, ok := h.requireDocumentsContext(w, r)
	if !ok {
		return
	}
	contentType, err := documents.ImageContentType(abs)
	if err != nil {
		mapDocumentsError(w, err)
		return
	}
	info, err := documents.StatRegular(abs)
	if err != nil {
		mapDocumentsError(w, err)
		return
	}
	if info.Size() > documents.DefaultImageMaxBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "file exceeds size cap")
		return
	}
	f, err := os.Open(abs)
	if err != nil {
		mapDocumentsError(w, err)
		return
	}
	defer f.Close()

	w.Header().Set("Content-Type", contentType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// Auth-gated content; do not let proxies share it across users.
	w.Header().Set("Cache-Control", "private, max-age=60")
	// http.ServeContent handles Range, ETag-via-mtime, and 304s without
	// duplicating that logic here. We've already set Content-Type
	// explicitly so it won't sniff.
	http.ServeContent(w, r, "", info.ModTime(), f)
}

// ---------------------------------------------------------------------------
// Write helpers & handlers (MUL-18)
// ---------------------------------------------------------------------------

// pkmAllowlistRootEnv is the env var name for the on-disk allowlist root that
// caps every workspace's pkm_path. Set on the server container to e.g. "/pkm".
const pkmAllowlistRootEnv = "MULTICA_PKM_ROOT"

// pkmDefaultPath is used when a workspace has no pkm_path set in settings.
// Mirrors the default declared in MUL-12 / MUL-14.
const pkmDefaultPath = "PKM-CUONG/GROWTH/PROJECTS"

// pkmConfirmHeader gates recursive folder deletes so a stray DELETE with
// ?force=true cannot wipe a tree without an explicit client opt-in.
const pkmConfirmHeader = "X-Confirm-Force-Delete"
const pkmConfirmValue = "yes"

// pkmMaxBodyBytes is the request-body cap for write/create. Mirrors the
// internal cap so we never read more than this into memory.
const pkmMaxBodyBytes = pkmfs.MaxFileBytes

// workspacePKMPathFromSettings extracts settings.pkm_path or falls back to
// the default. Settings is a JSONB column on workspace. Used by write
// handlers that open a pkmfs.FS per request.
func workspacePKMPathFromSettings(raw []byte) string {
	if len(raw) == 0 {
		return pkmDefaultPath
	}
	var settings map[string]any
	if err := json.Unmarshal(raw, &settings); err != nil {
		return pkmDefaultPath
	}
	if v, ok := settings["pkm_path"].(string); ok {
		v = strings.TrimSpace(v)
		v = strings.TrimPrefix(v, "/")
		if v != "" {
			return v
		}
	}
	return pkmDefaultPath
}

// pkmFSForWorkspace opens a *pkmfs.FS for the given workspace, reading
// allowlist root from env and pkm_path from workspace settings. Returns
// nil + an HTTP error already written on failure.
func (h *Handler) pkmFSForWorkspace(w http.ResponseWriter, r *http.Request, workspaceID string) *pkmfs.FS {
	allowRoot := strings.TrimSpace(os.Getenv(pkmAllowlistRootEnv))
	if allowRoot == "" {
		writeError(w, http.StatusServiceUnavailable, "pkm root not configured")
		return nil
	}

	ws, err := h.Queries.GetWorkspace(r.Context(), parseUUID(workspaceID))
	if err != nil {
		writeError(w, http.StatusNotFound, "workspace not found")
		return nil
	}

	base := workspacePKMPathFromSettings(ws.Settings)

	fs, err := pkmfs.New(allowRoot, base)
	if err != nil {
		switch {
		case errors.Is(err, pkmfs.ErrNotFound):
			writeError(w, http.StatusNotFound, "pkm path does not exist")
		case errors.Is(err, pkmfs.ErrTraversal), errors.Is(err, pkmfs.ErrInvalidPath), errors.Is(err, pkmfs.ErrNotFolder):
			writeError(w, http.StatusBadRequest, "pkm path invalid: "+err.Error())
		default:
			slog.Error("pkmfs open failed", append(logger.RequestAttrs(r), "error", err, "workspace_id", workspaceID)...)
			writeError(w, http.StatusInternalServerError, "pkm open failed")
		}
		return nil
	}
	return fs
}

// pkmRequiredPath reads the ?path= query, requires it to be non-empty.
func pkmRequiredPath(w http.ResponseWriter, r *http.Request) (string, bool) {
	p := r.URL.Query().Get("path")
	if strings.TrimSpace(p) == "" {
		writeError(w, http.StatusBadRequest, "path is required")
		return "", false
	}
	return p, true
}

func writePKMFSError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, pkmfs.ErrInvalidPath):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, pkmfs.ErrTraversal):
		slog.Warn("pkm traversal blocked", append(logger.RequestAttrs(r), "path", r.URL.Query().Get("path"))...)
		writeError(w, http.StatusBadRequest, "path escapes allowlist root")
	case errors.Is(err, pkmfs.ErrExtension), errors.Is(err, pkmfs.ErrNotMarkdown):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, pkmfs.ErrSymlink):
		writeError(w, http.StatusForbidden, err.Error())
	case errors.Is(err, pkmfs.ErrNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, pkmfs.ErrExist):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, pkmfs.ErrNotEmpty):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, pkmfs.ErrNotFolder):
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		slog.Error("pkmfs op failed", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusInternalServerError, "filesystem error")
	}
}

// readBoundedBody enforces the size cap and returns the bytes. The caller
// must already have set MaxBytesReader on r.Body for the same limit.
func readBoundedBody(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, pkmMaxBodyBytes)
	data, err := io.ReadAll(r.Body)
	if err != nil {
		// MaxBytesReader returns *http.MaxBytesError on overflow.
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeError(w, http.StatusRequestEntityTooLarge, "body exceeds 5 MB cap")
			return nil, false
		}
		writeError(w, http.StatusBadRequest, "could not read body")
		return nil, false
	}
	return data, true
}

// ---------------------------------------------------------------------------
// PUT /api/workspaces/{id}/documents/file?path=<rel>
// ---------------------------------------------------------------------------

func (h *Handler) PutDocumentFile(w http.ResponseWriter, r *http.Request) {
	workspaceID := workspaceIDFromURL(r, "id")
	rel, ok := pkmRequiredPath(w, r)
	if !ok {
		return
	}
	data, ok := readBoundedBody(w, r)
	if !ok {
		return
	}
	fs := h.pkmFSForWorkspace(w, r, workspaceID)
	if fs == nil {
		return
	}
	defer fs.Close()
	if err := fs.WriteFile(rel, data); err != nil {
		writePKMFSError(w, r, err)
		return
	}
	slog.Info("pkm file written", append(logger.RequestAttrs(r), "workspace_id", workspaceID, "path", rel, "bytes", len(data))...)
	writeJSON(w, http.StatusOK, map[string]any{"path": rel, "bytes": len(data)})
}

// ---------------------------------------------------------------------------
// POST /api/workspaces/{id}/documents/file?path=<rel>
// ---------------------------------------------------------------------------

func (h *Handler) CreateDocumentFile(w http.ResponseWriter, r *http.Request) {
	workspaceID := workspaceIDFromURL(r, "id")
	rel, ok := pkmRequiredPath(w, r)
	if !ok {
		return
	}
	data, ok := readBoundedBody(w, r)
	if !ok {
		return
	}
	fs := h.pkmFSForWorkspace(w, r, workspaceID)
	if fs == nil {
		return
	}
	defer fs.Close()
	if err := fs.CreateFile(rel, data); err != nil {
		writePKMFSError(w, r, err)
		return
	}
	slog.Info("pkm file created", append(logger.RequestAttrs(r), "workspace_id", workspaceID, "path", rel, "bytes", len(data))...)
	writeJSON(w, http.StatusCreated, map[string]any{"path": rel, "bytes": len(data)})
}

// ---------------------------------------------------------------------------
// POST /api/workspaces/{id}/documents/folder?path=<rel>
// ---------------------------------------------------------------------------

func (h *Handler) CreateDocumentFolder(w http.ResponseWriter, r *http.Request) {
	workspaceID := workspaceIDFromURL(r, "id")
	rel, ok := pkmRequiredPath(w, r)
	if !ok {
		return
	}
	fs := h.pkmFSForWorkspace(w, r, workspaceID)
	if fs == nil {
		return
	}
	defer fs.Close()
	if err := fs.CreateFolder(rel); err != nil {
		writePKMFSError(w, r, err)
		return
	}
	slog.Info("pkm folder created", append(logger.RequestAttrs(r), "workspace_id", workspaceID, "path", rel)...)
	writeJSON(w, http.StatusCreated, map[string]any{"path": rel})
}

// ---------------------------------------------------------------------------
// DELETE /api/workspaces/{id}/documents/file?path=<rel>
// ---------------------------------------------------------------------------

func (h *Handler) DeleteDocumentFile(w http.ResponseWriter, r *http.Request) {
	workspaceID := workspaceIDFromURL(r, "id")
	rel, ok := pkmRequiredPath(w, r)
	if !ok {
		return
	}
	fs := h.pkmFSForWorkspace(w, r, workspaceID)
	if fs == nil {
		return
	}
	defer fs.Close()
	if err := fs.DeleteFile(rel); err != nil {
		writePKMFSError(w, r, err)
		return
	}
	slog.Info("pkm file deleted", append(logger.RequestAttrs(r), "workspace_id", workspaceID, "path", rel)...)
	w.WriteHeader(http.StatusNoContent)
}

// ---------------------------------------------------------------------------
// DELETE /api/workspaces/{id}/documents/folder?path=<rel>[&force=true]
//
// Recursive delete (force=true) requires X-Confirm-Force-Delete: yes so an
// accidental query-string flag cannot wipe a directory tree.
// ---------------------------------------------------------------------------

func (h *Handler) DeleteDocumentFolder(w http.ResponseWriter, r *http.Request) {
	workspaceID := workspaceIDFromURL(r, "id")
	rel, ok := pkmRequiredPath(w, r)
	if !ok {
		return
	}
	force := strings.EqualFold(r.URL.Query().Get("force"), "true")
	if force && r.Header.Get(pkmConfirmHeader) != pkmConfirmValue {
		writeError(w, http.StatusPreconditionRequired, "force delete requires "+pkmConfirmHeader+": "+pkmConfirmValue)
		return
	}
	fs := h.pkmFSForWorkspace(w, r, workspaceID)
	if fs == nil {
		return
	}
	defer fs.Close()
	if err := fs.DeleteFolder(rel, force); err != nil {
		writePKMFSError(w, r, err)
		return
	}
	slog.Info("pkm folder deleted", append(logger.RequestAttrs(r), "workspace_id", workspaceID, "path", rel, "force", force)...)
	w.WriteHeader(http.StatusNoContent)
}
