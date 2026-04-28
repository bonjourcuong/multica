package handler

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	db "github.com/multica-ai/multica/server/pkg/db/generated"

	"github.com/multica-ai/multica/server/internal/documents"
)

func TestWorkspacePKMPathExtraction(t *testing.T) {
	cases := []struct {
		name string
		in   []byte
		want string
	}{
		{"nil", nil, ""},
		{"empty", []byte(``), ""},
		{"missing key", []byte(`{"other":"x"}`), ""},
		{"non-string", []byte(`{"pkm_path": 42}`), ""},
		{"happy", []byte(`{"pkm_path":"workspace1"}`), "workspace1"},
		{"trim", []byte(`{"pkm_path":"  workspace1  "}`), "workspace1"},
		{"invalid json", []byte(`{`), ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := workspacePKMPath(db.Workspace{Settings: c.in})
			if got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

func TestMapDocumentsErrorStatuses(t *testing.T) {
	cases := []struct {
		err  error
		code int
	}{
		{documents.ErrNotConfigured, http.StatusServiceUnavailable},
		{documents.ErrInvalidPath, http.StatusBadRequest},
		{documents.ErrOutsideRoot, http.StatusForbidden},
		{documents.ErrSymlinkEscape, http.StatusForbidden},
		{documents.ErrExtNotAllowed, http.StatusUnsupportedMediaType},
		{documents.ErrNotFound, http.StatusNotFound},
		{documents.ErrNotRegular, http.StatusBadRequest},
		{documents.ErrNotDirectory, http.StatusBadRequest},
		{documents.ErrTooLarge, http.StatusRequestEntityTooLarge},
		{os.ErrNotExist, http.StatusNotFound},
		{os.ErrPermission, http.StatusForbidden},
		{errors.New("boom"), http.StatusInternalServerError},
	}
	for _, c := range cases {
		w := httptest.NewRecorder()
		mapDocumentsError(w, c.err)
		if w.Code != c.code {
			t.Errorf("err=%v: got status %d, want %d", c.err, w.Code, c.code)
		}
	}
}

// TestDocumentsDisabledReturns503 covers the case where the server has not
// configured an allowlist root: every endpoint must respond 503 without
// touching the DB or filesystem.
func TestDocumentsDisabledReturns503(t *testing.T) {
	h := &Handler{} // h.Documents = nil
	for _, path := range []string{"/documents/tree", "/documents/file", "/documents/image"} {
		w := httptest.NewRecorder()
		req := httptest.NewRequest("GET", path, nil)
		var fn func(http.ResponseWriter, *http.Request)
		switch {
		case strings.HasSuffix(path, "/tree"):
			fn = h.GetDocumentsTree
		case strings.HasSuffix(path, "/file"):
			fn = h.GetDocumentsFile
		default:
			fn = h.GetDocumentsImage
		}
		fn(w, req)
		if w.Code != http.StatusServiceUnavailable {
			t.Errorf("%s: expected 503 with nil Documents, got %d", path, w.Code)
		}
	}
}

// TestResolveQueryDecoding documents the round-trip from a URL-encoded query
// string to the documents.Resolver. The HTTP layer normally URL-decodes the
// query for us, so by the time Resolve runs the bytes are the literal
// traversal sequence ".." and the resolver rejects them. This is the sanity
// check that the existing rejection set actually blocks what an attacker
// would send over the wire.
func TestResolveQueryDecoding(t *testing.T) {
	tmp := t.TempDir()
	root := filepath.Join(tmp, "root")
	if err := os.MkdirAll(filepath.Join(root, "ws"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "outside.md"), []byte("x"), 0o644); err != nil {
		t.Fatalf("seed outside: %v", err)
	}
	r, err := documents.NewResolver(root)
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}

	// Things a hostile client might put in ?path= over the wire. After the
	// HTTP server URL-decodes the query, these are the literal strings the
	// handler would pass to Resolve.
	encoded := []string{
		"%2E%2E%2Foutside.md",   // ../outside.md
		"%2e%2e%2foutside.md",   // ../outside.md (lowercase)
		"%2f..%2f..%2foutside",  // /../../outside
		"foo%00bar",              // foo<NUL>bar
	}
	for _, raw := range encoded {
		dec, err := url.QueryUnescape(raw)
		if err != nil {
			t.Fatalf("decode %q: %v", raw, err)
		}
		if _, err := r.Resolve("ws", dec); err == nil {
			t.Errorf("decoded %q (%q): expected rejection, got nil", raw, dec)
		}
	}
}
