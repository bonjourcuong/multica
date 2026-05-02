package secrets

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFromDirOrEnv(t *testing.T) {
	t.Run("file content wins over env", func(t *testing.T) {
		dir := t.TempDir()
		writeSecret(t, dir, "jwt_secret", "from-file")
		t.Setenv("JWT_SECRET", "from-env")

		if got := fromDirOrEnv(dir, "JWT_SECRET"); got != "from-file" {
			t.Errorf("got %q, want %q", got, "from-file")
		}
	})

	t.Run("falls back to env when file missing", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("JWT_SECRET", "from-env")

		if got := fromDirOrEnv(dir, "JWT_SECRET"); got != "from-env" {
			t.Errorf("got %q, want %q", got, "from-env")
		}
	})

	t.Run("returns empty string when both absent", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("JWT_SECRET", "")

		if got := fromDirOrEnv(dir, "JWT_SECRET"); got != "" {
			t.Errorf("got %q, want empty", got)
		}
	})

	t.Run("trims trailing newline from file", func(t *testing.T) {
		dir := t.TempDir()
		writeSecret(t, dir, "jwt_secret", "from-file\n")

		if got := fromDirOrEnv(dir, "JWT_SECRET"); got != "from-file" {
			t.Errorf("got %q, want %q", got, "from-file")
		}
	})

	t.Run("preserves leading and internal whitespace", func(t *testing.T) {
		dir := t.TempDir()
		writeSecret(t, dir, "jwt_secret", " padded value ")

		if got := fromDirOrEnv(dir, "JWT_SECRET"); got != " padded value " {
			t.Errorf("got %q, want %q", got, " padded value ")
		}
	})

	t.Run("name is lowercased to find the file", func(t *testing.T) {
		dir := t.TempDir()
		writeSecret(t, dir, "resend_api_key", "rk_test_123")
		t.Setenv("RESEND_API_KEY", "")

		if got := fromDirOrEnv(dir, "RESEND_API_KEY"); got != "rk_test_123" {
			t.Errorf("got %q, want %q", got, "rk_test_123")
		}
	})

	t.Run("empty file beats env (file presence is the signal)", func(t *testing.T) {
		dir := t.TempDir()
		writeSecret(t, dir, "jwt_secret", "")
		t.Setenv("JWT_SECRET", "from-env")

		if got := fromDirOrEnv(dir, "JWT_SECRET"); got != "" {
			t.Errorf("got %q, want empty (file present should win)", got)
		}
	})
}

func writeSecret(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write secret %q: %v", path, err)
	}
}
