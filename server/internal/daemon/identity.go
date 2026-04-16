package daemon

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
)

// DaemonIDFileName is the per-profile file that stores the persistent daemon UUID.
const DaemonIDFileName = "daemon.id"

// LoadOrCreateDaemonID returns the persistent daemon identifier stored under
// stateDir. On first run the file does not exist; a fresh v4 UUID is generated,
// written atomically, and returned. On subsequent runs the existing value is
// read verbatim — so hostname drift (.local suffix, system rename, profile
// switch) can no longer produce a second agent_runtime row.
//
// stateDir is the profile-specific directory (e.g. ~/.multica or
// ~/.multica/profiles/<name>). The caller is expected to create it before
// invoking this function; an MkdirAll fallback is kept for defensive callers
// that forgot.
func LoadOrCreateDaemonID(stateDir string) (string, bool, error) {
	if strings.TrimSpace(stateDir) == "" {
		return "", false, errors.New("daemon identity: stateDir is empty")
	}

	path := filepath.Join(stateDir, DaemonIDFileName)
	data, err := os.ReadFile(path)
	if err == nil {
		id := strings.TrimSpace(string(data))
		if _, parseErr := uuid.Parse(id); parseErr != nil {
			return "", false, fmt.Errorf("daemon identity: %s contains invalid UUID %q: %w", path, id, parseErr)
		}
		return id, false, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "", false, fmt.Errorf("daemon identity: read %s: %w", path, err)
	}

	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return "", false, fmt.Errorf("daemon identity: create %s: %w", stateDir, err)
	}

	newID := uuid.NewString()

	tmp, err := os.CreateTemp(stateDir, ".daemon-id-*.tmp")
	if err != nil {
		return "", false, fmt.Errorf("daemon identity: create temp file: %w", err)
	}
	tmpPath := tmp.Name()
	if _, err := tmp.WriteString(newID + "\n"); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return "", false, fmt.Errorf("daemon identity: write temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return "", false, fmt.Errorf("daemon identity: close temp file: %w", err)
	}
	if err := os.Chmod(tmpPath, 0o600); err != nil {
		os.Remove(tmpPath)
		return "", false, fmt.Errorf("daemon identity: chmod temp file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return "", false, fmt.Errorf("daemon identity: rename temp file: %w", err)
	}
	return newID, true, nil
}

// LegacyDaemonIDCandidates returns the set of daemon_id values this machine
// may have produced before UUID persistence landed. The server uses them to
// locate and merge stale agent_runtime rows so existing agents keep working
// without manual migration.
//
// The historical formats covered:
//   - <hostname>                      (current, post-#1070)
//   - <hostname>.local                (pre-#1070 macOS bonjour suffix)
//   - <hostname>-<profile>            (pre-#906, profile-suffixed)
//   - <hostname>.local-<profile>      (the .local + profile suffix combo)
//
// Duplicates and empty strings are removed. Order is preserved so the caller
// sees the most-likely-current form first.
func LegacyDaemonIDCandidates(hostname, profile string) []string {
	hostname = strings.TrimSpace(hostname)
	if hostname == "" {
		return nil
	}

	stripped := strings.TrimSuffix(hostname, ".local")

	raw := []string{hostname, stripped}
	if profile != "" {
		raw = append(raw, hostname+"-"+profile, stripped+"-"+profile)
	}

	seen := make(map[string]struct{}, len(raw))
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if _, dup := seen[v]; dup {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}
