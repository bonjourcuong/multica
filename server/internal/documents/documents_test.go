package documents

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// setupRoot creates an allowlist root + workspace pkm dir with some seeded
// files. Layout under tmp:
//
//	root/
//	  workspace1/        <-- workspace pkm_path
//	    notes/
//	      hello.md
//	      photo.png
//	    secret.md
//	    hidden.txt       <-- non-allowlist extension
//	    .dotfile         <-- hidden, must not show up in listings
//	  outside.md         <-- in root but outside workspace pkm_path
//	sibling/
//	  evil.md            <-- outside root entirely
func setupRoot(t *testing.T) (root, workspaceRel string) {
	t.Helper()
	tmp := t.TempDir()
	root = filepath.Join(tmp, "root")
	if err := os.MkdirAll(filepath.Join(root, "workspace1", "notes"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(tmp, "sibling"), 0o755); err != nil {
		t.Fatalf("mkdir sibling: %v", err)
	}
	writeFile(t, filepath.Join(root, "workspace1", "notes", "hello.md"), "# hello\n")
	writeFile(t, filepath.Join(root, "workspace1", "notes", "photo.png"), "\x89PNG fake")
	writeFile(t, filepath.Join(root, "workspace1", "secret.md"), "secret")
	writeFile(t, filepath.Join(root, "workspace1", "hidden.txt"), "nope")
	writeFile(t, filepath.Join(root, "workspace1", ".dotfile"), "hidden")
	writeFile(t, filepath.Join(root, "outside.md"), "outside the workspace")
	writeFile(t, filepath.Join(tmp, "sibling", "evil.md"), "evil")
	return root, "workspace1"
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestNewResolverRejectsBadRoots(t *testing.T) {
	if _, err := NewResolver(""); !errors.Is(err, ErrNotConfigured) {
		t.Errorf("empty root: got %v, want ErrNotConfigured", err)
	}
	if _, err := NewResolver("relative/path"); err == nil {
		t.Errorf("relative root: expected error, got nil")
	}
	if _, err := NewResolver("/this/path/does/not/exist/123abc"); err == nil {
		t.Errorf("nonexistent root: expected error, got nil")
	}

	tmp := t.TempDir()
	f := filepath.Join(tmp, "regular-file")
	writeFile(t, f, "")
	if _, err := NewResolver(f); err == nil {
		t.Errorf("file as root: expected error, got nil")
	}
}

func TestResolveTraversalVectors(t *testing.T) {
	root, ws := setupRoot(t)
	r, err := NewResolver(root)
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}

	// Each vector must be rejected as ErrInvalidPath, ErrOutsideRoot, or
	// ErrSymlinkEscape. We don't pin the exact error class — the contract
	// is "do not return a path".
	traversal := []string{
		"..",
		"../",
		"../outside.md",
		"../../etc/passwd",
		"notes/../../outside.md",
		"notes/../../../etc/passwd",
		"./../outside.md",
		"foo/./../../bar",
		"/etc/passwd",
		"/absolute/leaks",
		`\windows\system32`,
		"C:/abs",
		"C:\\abs",
		"foo\x00bar",
		"notes/../..",
	}
	for _, v := range traversal {
		t.Run("reject "+v, func(t *testing.T) {
			_, err := r.Resolve(ws, v)
			if err == nil {
				t.Fatalf("expected error for %q, got nil", v)
			}
			if !(errors.Is(err, ErrInvalidPath) || errors.Is(err, ErrOutsideRoot) || errors.Is(err, ErrSymlinkEscape)) {
				t.Fatalf("unexpected error class for %q: %v", v, err)
			}
		})
	}
}

func TestResolveAcceptsValidRelativePaths(t *testing.T) {
	root, ws := setupRoot(t)
	r, err := NewResolver(root)
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}

	cases := map[string]string{
		"":                 filepath.Join(root, "workspace1"),
		".":                filepath.Join(root, "workspace1"),
		"notes":            filepath.Join(root, "workspace1", "notes"),
		"notes/":           filepath.Join(root, "workspace1", "notes"),
		"notes/hello.md":   filepath.Join(root, "workspace1", "notes", "hello.md"),
		"./notes/hello.md": filepath.Join(root, "workspace1", "notes", "hello.md"),
		"secret.md":        filepath.Join(root, "workspace1", "secret.md"),
		// missing path is allowed (falls through to fs ENOENT later); the
		// resolver only guarantees containment.
		"notes/missing.md": filepath.Join(root, "workspace1", "notes", "missing.md"),
	}
	for in, want := range cases {
		got, err := r.Resolve(ws, in)
		if err != nil {
			t.Errorf("Resolve(%q): unexpected error %v", in, err)
			continue
		}
		if got != filepath.Clean(want) {
			t.Errorf("Resolve(%q) = %q, want %q", in, got, filepath.Clean(want))
		}
	}
}

func TestResolveRejectsBadPkmPath(t *testing.T) {
	root, _ := setupRoot(t)
	r, err := NewResolver(root)
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	bad := []string{
		"",
		"..",
		"../sibling",
		"/etc",
		"workspace1/../..",
	}
	for _, v := range bad {
		if _, err := r.Resolve(v, "."); err == nil {
			t.Errorf("Resolve(pkm=%q): expected error, got nil", v)
		}
	}
}

func TestResolveSymlinkEscape(t *testing.T) {
	root, ws := setupRoot(t)
	tmpRoot := filepath.Dir(root)

	// Create a symlink inside the workspace pointing OUT of the root.
	target := filepath.Join(tmpRoot, "sibling")
	link := filepath.Join(root, "workspace1", "escape")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}

	r, err := NewResolver(root)
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	if _, err := r.Resolve(ws, "escape"); !errors.Is(err, ErrSymlinkEscape) {
		t.Errorf("symlink escape: got %v, want ErrSymlinkEscape", err)
	}
	// Following through the symlink to a file inside its target must also
	// be rejected.
	if _, err := r.Resolve(ws, "escape/evil.md"); !errors.Is(err, ErrSymlinkEscape) {
		t.Errorf("symlink escape via subpath: got %v, want ErrSymlinkEscape", err)
	}
}

func TestResolveInternalSymlinkOK(t *testing.T) {
	root, ws := setupRoot(t)
	link := filepath.Join(root, "workspace1", "alias-notes")
	target := filepath.Join(root, "workspace1", "notes")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}
	r, err := NewResolver(root)
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	got, err := r.Resolve(ws, "alias-notes/hello.md")
	if err != nil {
		t.Fatalf("internal symlink rejected: %v", err)
	}
	want := filepath.Join(root, "workspace1", "notes", "hello.md")
	if got != want {
		t.Errorf("internal symlink: got %q, want %q", got, want)
	}
}

// TestRootIsSymlinkResolved makes sure that an allowlist root provided as a
// symlink is normalized: a "sibling" path that prefix-matches the symlink
// name but not the resolved target must not slip through.
func TestRootIsSymlinkResolved(t *testing.T) {
	tmp := t.TempDir()
	realRoot := filepath.Join(tmp, "real")
	if err := os.Mkdir(realRoot, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	link := filepath.Join(tmp, "link")
	if err := os.Symlink(realRoot, link); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}
	r, err := NewResolver(link)
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	if r.Root() != filepath.Clean(realRoot) {
		t.Errorf("root not eval-symlinked: got %q, want %q", r.Root(), realRoot)
	}
}

// TestSiblingPrefixCollision checks the trailing-separator guard: a directory
// named "rooty" must not be treated as inside "/.../root".
func TestSiblingPrefixCollision(t *testing.T) {
	tmp := t.TempDir()
	root := filepath.Join(tmp, "rt")
	rootSibling := filepath.Join(tmp, "rt-evil")
	for _, d := range []string{root, rootSibling} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}
	r, err := NewResolver(root)
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	// isInside(root, root-sibling) must be false.
	if isInside(r.Root(), rootSibling) {
		t.Errorf("isInside accepted sibling %q under %q", rootSibling, r.Root())
	}
}

func TestListDirHidesDotfilesAndDangling(t *testing.T) {
	root, ws := setupRoot(t)
	r, err := NewResolver(root)
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}

	// Add a dangling symlink.
	if err := os.Symlink(filepath.Join(root, "does-not-exist"), filepath.Join(root, "workspace1", "broken")); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}
	// Add an escaping symlink — must be filtered from listings.
	if err := os.Symlink(filepath.Dir(root), filepath.Join(root, "workspace1", "escape-link")); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}

	abs, err := r.Resolve(ws, ".")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	entries, err := r.ListDir(abs)
	if err != nil {
		t.Fatalf("ListDir: %v", err)
	}
	names := map[string]Entry{}
	for _, e := range entries {
		names[e.Name] = e
	}
	if _, ok := names[".dotfile"]; ok {
		t.Errorf("dotfile leaked into listing")
	}
	if _, ok := names["broken"]; ok {
		t.Errorf("dangling symlink leaked into listing")
	}
	if _, ok := names["escape-link"]; ok {
		t.Errorf("escaping symlink leaked into listing")
	}
	if _, ok := names["notes"]; !ok || names["notes"].Type != "dir" {
		t.Errorf("expected notes dir entry, got %+v", names["notes"])
	}
	if _, ok := names["secret.md"]; !ok || names["secret.md"].Type != "file" {
		t.Errorf("expected secret.md file entry, got %+v", names["secret.md"])
	}
}

func TestListDirOnFileFails(t *testing.T) {
	root, ws := setupRoot(t)
	r, err := NewResolver(root)
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	abs, err := r.Resolve(ws, "secret.md")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if _, err := r.ListDir(abs); !errors.Is(err, ErrNotDirectory) {
		t.Errorf("ListDir on file: got %v, want ErrNotDirectory", err)
	}
}

func TestReadMarkdownExtensionEnforced(t *testing.T) {
	root, ws := setupRoot(t)
	r, err := NewResolver(root)
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	hidden, _ := r.Resolve(ws, "hidden.txt")
	if _, _, err := ReadMarkdown(hidden, 0); !errors.Is(err, ErrExtNotAllowed) {
		t.Errorf("non-md: got %v, want ErrExtNotAllowed", err)
	}
	png, _ := r.Resolve(ws, "notes/photo.png")
	if _, _, err := ReadMarkdown(png, 0); !errors.Is(err, ErrExtNotAllowed) {
		t.Errorf("png as md: got %v, want ErrExtNotAllowed", err)
	}
}

func TestReadMarkdownSizeCap(t *testing.T) {
	root, ws := setupRoot(t)
	r, err := NewResolver(root)
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	big := filepath.Join(root, "workspace1", "big.md")
	writeFile(t, big, strings.Repeat("a", 1024))
	abs, _ := r.Resolve(ws, "big.md")
	if _, _, err := ReadMarkdown(abs, 100); !errors.Is(err, ErrTooLarge) {
		t.Errorf("size cap: got %v, want ErrTooLarge", err)
	}
	if _, _, err := ReadMarkdown(abs, 4096); err != nil {
		t.Errorf("under cap: unexpected error %v", err)
	}
}

func TestReadMarkdownHappyPath(t *testing.T) {
	root, ws := setupRoot(t)
	r, err := NewResolver(root)
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	abs, err := r.Resolve(ws, "notes/hello.md")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	data, mtime, err := ReadMarkdown(abs, 0)
	if err != nil {
		t.Fatalf("ReadMarkdown: %v", err)
	}
	if string(data) != "# hello\n" {
		t.Errorf("content mismatch: got %q", data)
	}
	if mtime.IsZero() {
		t.Errorf("mtime not populated")
	}
}

func TestImageContentType(t *testing.T) {
	cases := map[string]string{
		"foo.png":   "image/png",
		"foo.PNG":   "image/png",
		"foo.jpg":   "image/jpeg",
		"foo.JPEG":  "image/jpeg",
		"foo.gif":   "image/gif",
		"foo.webp":  "image/webp",
		"path/p.gif": "image/gif",
	}
	for in, want := range cases {
		got, err := ImageContentType(in)
		if err != nil {
			t.Errorf("ImageContentType(%q): unexpected error %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("ImageContentType(%q) = %q, want %q", in, got, want)
		}
	}
	// SVG is intentionally NOT allowed yet (sanitizer not wired).
	for _, in := range []string{"foo.svg", "foo.txt", "foo.md", "foo.bmp", "foo.tiff", "foo"} {
		if _, err := ImageContentType(in); !errors.Is(err, ErrExtNotAllowed) {
			t.Errorf("ImageContentType(%q): got %v, want ErrExtNotAllowed", in, err)
		}
	}
}

func TestStatRegularRejectsDirectory(t *testing.T) {
	root, ws := setupRoot(t)
	r, err := NewResolver(root)
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	abs, _ := r.Resolve(ws, "notes")
	if _, err := StatRegular(abs); !errors.Is(err, ErrNotRegular) {
		t.Errorf("StatRegular on dir: got %v, want ErrNotRegular", err)
	}
}

func TestStatRegularNotFound(t *testing.T) {
	root, ws := setupRoot(t)
	r, err := NewResolver(root)
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	abs, _ := r.Resolve(ws, "ghost.md")
	if _, err := StatRegular(abs); !errors.Is(err, ErrNotFound) {
		t.Errorf("StatRegular missing: got %v, want ErrNotFound", err)
	}
}

// Encoded vectors: a real HTTP handler will URL-decode the query string
// before calling Resolve, so by the time it lands here the bytes look
// either like the literal ".." or are passed through verbatim. We assert
// both shapes are rejected.
func TestEncodedTraversalShape(t *testing.T) {
	root, ws := setupRoot(t)
	r, err := NewResolver(root)
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	// %2e%2e%2f → ../  (decoded form, what the handler sees)
	if _, err := r.Resolve(ws, "../outside.md"); err == nil {
		t.Errorf("decoded %%2e%%2e%%2f vector accepted")
	}
	// Double-encoded %252e%252e%252f decodes once to %2e%2e%2f, which is
	// then a literal three-segment-looking string (no actual ".." after
	// Clean). We accept it as a name on disk if it happens to exist; the
	// security guarantee is that no escape happens. Verify the resolver
	// keeps the path inside the root regardless.
	got, err := r.Resolve(ws, "%2e%2e%2foutside.md")
	if err != nil {
		// either rejection or containment is acceptable
		return
	}
	if !isInside(r.Root(), got) {
		t.Errorf("double-encoded vector escaped root: %q", got)
	}
}
