package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"sort"
	"strings"

	"github.com/multica-ai/multica/server/internal/documents"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// pkmPathSettingKey is the key used inside the workspace `settings` JSON
// column to hold the workspace's PKM root (a path RELATIVE to the server
// allowlist root). MUL-14 will eventually move this into a dedicated column;
// reading from settings here keeps PR3 unblocked and makes that migration a
// localized change to workspacePKMPath().
const pkmPathSettingKey = "pkm_path"

// workspacePKMPath extracts the configured pkm_path for a workspace from its
// settings JSON. Returns "" when not configured.
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
	return strings.TrimSpace(s)
}

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
		writeError(w, http.StatusServiceUnavailable, "pkm_path not configured for this workspace")
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
	Path    string             `json:"path"`
	Entries []documents.Entry  `json:"entries"`
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
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].Type != entries[j].Type {
			return entries[i].Type == "dir"
		}
		return entries[i].Name < entries[j].Name
	})

	rel := strings.TrimSpace(r.URL.Query().Get("path"))
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
