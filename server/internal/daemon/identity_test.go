package daemon

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
)

func TestLoadOrCreateDaemonID_CreatesAndReads(t *testing.T) {
	dir := t.TempDir()

	id, created, err := LoadOrCreateDaemonID(dir)
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	if !created {
		t.Fatalf("expected created=true on fresh directory")
	}
	if _, err := uuid.Parse(id); err != nil {
		t.Fatalf("first call returned invalid UUID %q: %v", id, err)
	}

	id2, created, err := LoadOrCreateDaemonID(dir)
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if created {
		t.Fatalf("expected created=false when daemon.id exists")
	}
	if id != id2 {
		t.Fatalf("expected stable id across calls: got %q then %q", id, id2)
	}

	path := filepath.Join(dir, DaemonIDFileName)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if string(data) == "" {
		t.Fatalf("daemon.id is empty")
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("expected 0600 perms, got %v", info.Mode().Perm())
	}
}

func TestLoadOrCreateDaemonID_RejectsCorruptFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, DaemonIDFileName)
	if err := os.WriteFile(path, []byte("not-a-uuid"), 0o600); err != nil {
		t.Fatalf("setup: %v", err)
	}

	_, _, err := LoadOrCreateDaemonID(dir)
	if err == nil {
		t.Fatalf("expected error on non-UUID contents, got nil")
	}
}

func TestLoadOrCreateDaemonID_EmptyDir(t *testing.T) {
	if _, _, err := LoadOrCreateDaemonID(""); err == nil {
		t.Fatalf("expected error for empty stateDir")
	}
}

func TestLegacyDaemonIDCandidates(t *testing.T) {
	cases := []struct {
		name     string
		hostname string
		profile  string
		want     []string
	}{
		{
			name:     "plain hostname, default profile",
			hostname: "MacBook-Pro",
			profile:  "",
			want:     []string{"MacBook-Pro"},
		},
		{
			name:     ".local hostname, default profile",
			hostname: "Jiayuans-MacBook-Pro.local",
			profile:  "",
			want:     []string{"Jiayuans-MacBook-Pro.local", "Jiayuans-MacBook-Pro"},
		},
		{
			name:     "plain hostname, named profile",
			hostname: "MacBook-Air",
			profile:  "staging",
			want:     []string{"MacBook-Air", "MacBook-Air-staging"},
		},
		{
			name:     ".local hostname, named profile",
			hostname: "Jiayuans-MacBook-Pro.local",
			profile:  "staging",
			want: []string{
				"Jiayuans-MacBook-Pro.local",
				"Jiayuans-MacBook-Pro",
				"Jiayuans-MacBook-Pro.local-staging",
				"Jiayuans-MacBook-Pro-staging",
			},
		},
		{
			name:     "empty hostname returns nil",
			hostname: "",
			profile:  "staging",
			want:     nil,
		},
		{
			name:     "whitespace-only hostname",
			hostname: "   ",
			profile:  "",
			want:     nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := LegacyDaemonIDCandidates(tc.hostname, tc.profile)
			if !equalStringSlice(got, tc.want) {
				t.Fatalf("LegacyDaemonIDCandidates(%q, %q) = %v, want %v", tc.hostname, tc.profile, got, tc.want)
			}
		})
	}
}

func equalStringSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
