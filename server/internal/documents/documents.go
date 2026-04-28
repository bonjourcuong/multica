// Package documents implements the read-only filesystem access layer for the
// Documents tab (PKM browser). It enforces an allowlisted server-side root,
// per-workspace pkm_path scoping, path-traversal defense, and symlink-escape
// rejection.
//
// All paths exposed to clients are relative to the workspace's pkm_path, which
// itself is relative to the allowlisted root. The Resolver guarantees that no
// resolution can escape the root, even via symlinks created inside it.
package documents

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Errors surfaced to handlers. They map to HTTP statuses there:
//
//	ErrNotConfigured   → 503 Service Unavailable
//	ErrInvalidPath     → 400 Bad Request
//	ErrOutsideRoot     → 403 Forbidden
//	ErrSymlinkEscape   → 403 Forbidden
//	ErrExtNotAllowed   → 415 Unsupported Media Type
//	ErrNotFound        → 404 Not Found
//	ErrNotRegular      → 400 Bad Request
//	ErrTooLarge        → 413 Payload Too Large
var (
	ErrNotConfigured = errors.New("documents: not configured")
	ErrInvalidPath   = errors.New("documents: invalid path")
	ErrOutsideRoot   = errors.New("documents: path outside allowlist root")
	ErrSymlinkEscape = errors.New("documents: symlink escapes allowlist root")
	ErrExtNotAllowed = errors.New("documents: file extension not allowed")
	ErrNotFound      = errors.New("documents: not found")
	ErrNotRegular    = errors.New("documents: not a regular file")
	ErrNotDirectory  = errors.New("documents: not a directory")
	ErrTooLarge      = errors.New("documents: file exceeds size cap")
)

// Default size caps. Markdown files are small in practice (PKM notes), images
// are bounded so a malicious or unexpected huge file cannot blow up memory or
// the response. Override via NewResolver options if needed.
const (
	DefaultMarkdownMaxBytes = 4 << 20  // 4 MiB
	DefaultImageMaxBytes    = 25 << 20 // 25 MiB
)

// Resolver enforces the security boundary. It is safe for concurrent use.
type Resolver struct {
	// rootEval is the absolute, symlink-resolved allowlist root. All paths
	// returned by Resolve are guaranteed to live inside it.
	rootEval string
}

// NewResolver returns a Resolver bound to root. root must be an absolute path
// to an existing directory; symlinks in the path are resolved at construction
// time so all later prefix checks compare against the canonical filesystem
// location, not a possibly-replaced symlink target.
//
// Returns ErrNotConfigured when root is empty (feature disabled).
func NewResolver(root string) (*Resolver, error) {
	if strings.TrimSpace(root) == "" {
		return nil, ErrNotConfigured
	}
	if !filepath.IsAbs(root) {
		return nil, fmt.Errorf("documents: root must be absolute, got %q", root)
	}
	eval, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, fmt.Errorf("documents: cannot resolve root: %w", err)
	}
	info, err := os.Stat(eval)
	if err != nil {
		return nil, fmt.Errorf("documents: cannot stat root: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("documents: root is not a directory: %s", eval)
	}
	return &Resolver{rootEval: filepath.Clean(eval)}, nil
}

// Root returns the canonical allowlist root.
func (r *Resolver) Root() string { return r.rootEval }

// Resolve combines the workspace's pkm_path (a directory under the allowlist
// root, configured by an admin) with a request-supplied relative path and
// returns the absolute on-disk path.
//
// Both pkmPath and relPath must be relative and must not contain ".." segments.
// The final resolved path must be a descendant of the allowlist root, both
// before and after symlink resolution. relPath may be empty to refer to the
// workspace root itself.
func (r *Resolver) Resolve(pkmPath, relPath string) (string, error) {
	if r == nil {
		return "", ErrNotConfigured
	}
	if pkmPath == "" {
		return "", fmt.Errorf("%w: pkm_path not configured", ErrInvalidPath)
	}
	cleanPkm, err := cleanRelative(pkmPath)
	if err != nil {
		return "", fmt.Errorf("%w: invalid pkm_path: %v", ErrInvalidPath, err)
	}
	cleanRel, err := cleanRelative(relPath)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidPath, err)
	}

	joined := filepath.Join(r.rootEval, cleanPkm, cleanRel)
	joined = filepath.Clean(joined)
	if !isInside(r.rootEval, joined) {
		return "", ErrOutsideRoot
	}

	// Symlink-escape defense: if the path exists, resolve any symlinks along
	// the way and re-check the prefix. EvalSymlinks fails on missing paths;
	// for ENOENT we accept the cleaned path as-is (it has already been
	// prefix-checked). Any other error is surfaced.
	resolved, err := filepath.EvalSymlinks(joined)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return joined, nil
		}
		return "", fmt.Errorf("%w: %v", ErrInvalidPath, err)
	}
	if !isInside(r.rootEval, resolved) {
		return "", ErrSymlinkEscape
	}
	return resolved, nil
}

// cleanRelative validates and normalizes a user-provided relative path. It
// rejects absolute paths, embedded NUL bytes, and any path that resolves
// upward through "..".
//
// Empty input is allowed and returns ".".
func cleanRelative(p string) (string, error) {
	if strings.ContainsRune(p, 0) {
		return "", errors.New("contains NUL byte")
	}
	// Reject Windows-style drive letters and UNC prefixes defensively even
	// on Unix; cheap and avoids surprises if this ever runs on Windows.
	if len(p) >= 2 && p[1] == ':' {
		return "", errors.New("absolute path not allowed")
	}
	if filepath.IsAbs(p) || strings.HasPrefix(p, "/") || strings.HasPrefix(p, `\`) {
		return "", errors.New("absolute path not allowed")
	}
	cleaned := filepath.Clean(p)
	if cleaned == "" {
		cleaned = "."
	}
	if cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return "", errors.New("path escapes its base")
	}
	// filepath.Clean handles internal "..", but if it remains the path is
	// trying to escape. Belt and suspenders.
	for _, seg := range strings.Split(cleaned, string(filepath.Separator)) {
		if seg == ".." {
			return "", errors.New("path contains '..' segment")
		}
	}
	return cleaned, nil
}

// isInside reports whether descendant is at or below ancestor on the
// filesystem. Both arguments must already be cleaned and absolute.
//
// We compare with a trailing separator on the ancestor so "/srv/pkm-evil"
// is not accepted as living under "/srv/pkm".
func isInside(ancestor, descendant string) bool {
	if ancestor == descendant {
		return true
	}
	a := ancestor
	if !strings.HasSuffix(a, string(filepath.Separator)) {
		a += string(filepath.Separator)
	}
	return strings.HasPrefix(descendant, a)
}

// Entry describes one item in a directory listing.
type Entry struct {
	Name    string    `json:"name"`
	Type    string    `json:"type"` // "dir" or "file"
	Size    int64     `json:"size"`
	ModTime time.Time `json:"mtime"`
}

// ListDir lists the directory at absPath. absPath must already be the result
// of Resolver.Resolve; ListDir does not perform any sandboxing of its own.
//
// Symlink entries are included only if their target stays inside the
// allowlist root according to the Resolver.
func (r *Resolver) ListDir(absPath string) ([]Entry, error) {
	info, err := os.Lstat(absPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if !info.IsDir() {
		return nil, ErrNotDirectory
	}
	raw, err := os.ReadDir(absPath)
	if err != nil {
		return nil, err
	}
	out := make([]Entry, 0, len(raw))
	for _, d := range raw {
		// Hide dotfiles (e.g. .git) — PKM users don't expect to browse them
		// and it removes a class of footguns.
		if strings.HasPrefix(d.Name(), ".") {
			continue
		}
		full := filepath.Join(absPath, d.Name())

		// For symlinks, check the target stays inside the root before
		// exposing it. Lstat first to avoid following a dangling link.
		li, err := os.Lstat(full)
		if err != nil {
			continue
		}
		if li.Mode()&os.ModeSymlink != 0 {
			resolved, err := filepath.EvalSymlinks(full)
			if err != nil || !isInside(r.rootEval, resolved) {
				continue
			}
			// Re-stat through the symlink so the entry shape (file/dir,
			// size) reflects the target.
			ti, err := os.Stat(resolved)
			if err != nil {
				continue
			}
			out = append(out, entryFor(d.Name(), ti))
			continue
		}
		out = append(out, entryFor(d.Name(), li))
	}
	return out, nil
}

func entryFor(name string, info fs.FileInfo) Entry {
	t := "file"
	if info.IsDir() {
		t = "dir"
	}
	return Entry{
		Name:    name,
		Type:    t,
		Size:    info.Size(),
		ModTime: info.ModTime().UTC(),
	}
}

// markdownExt is the only file extension served by ReadMarkdown.
const markdownExt = ".md"

// imageExts maps allowed image extensions to their content type. SVG is
// intentionally excluded: it can carry inline scripts and we have no
// sanitizer wired up yet (tracked for a follow-up). PR3 keeps to raster
// formats only.
var imageExts = map[string]string{
	".png":  "image/png",
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".gif":  "image/gif",
	".webp": "image/webp",
}

// ReadMarkdown reads a `.md` file. It rejects any other extension with
// ErrExtNotAllowed and any file larger than maxBytes with ErrTooLarge.
// Returns the raw bytes and the file mtime.
func ReadMarkdown(absPath string, maxBytes int64) ([]byte, time.Time, error) {
	if !strings.EqualFold(filepath.Ext(absPath), markdownExt) {
		return nil, time.Time{}, ErrExtNotAllowed
	}
	if maxBytes <= 0 {
		maxBytes = DefaultMarkdownMaxBytes
	}
	return readBoundedRegular(absPath, maxBytes)
}

// ImageContentType returns the canonical content-type for an allowed image
// extension, or "" + ErrExtNotAllowed otherwise.
func ImageContentType(absPath string) (string, error) {
	ct, ok := imageExts[strings.ToLower(filepath.Ext(absPath))]
	if !ok {
		return "", ErrExtNotAllowed
	}
	return ct, nil
}

// StatRegular returns FileInfo for a regular file at absPath. Returns
// ErrNotFound, ErrNotRegular as appropriate.
func StatRegular(absPath string) (fs.FileInfo, error) {
	info, err := os.Stat(absPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, ErrNotRegular
	}
	return info, nil
}

// readBoundedRegular reads a regular file up to maxBytes. Returns ErrTooLarge
// if the file is larger; ErrNotRegular for non-regular targets.
func readBoundedRegular(absPath string, maxBytes int64) ([]byte, time.Time, error) {
	info, err := StatRegular(absPath)
	if err != nil {
		return nil, time.Time{}, err
	}
	if info.Size() > maxBytes {
		return nil, time.Time{}, ErrTooLarge
	}
	data, err := os.ReadFile(absPath)
	if err != nil {
		return nil, time.Time{}, err
	}
	return data, info.ModTime().UTC(), nil
}
