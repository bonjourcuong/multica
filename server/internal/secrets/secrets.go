// Package secrets resolves sensitive runtime values from Docker file-backed
// secrets with a fallback to environment variables. File-backed secrets keep
// values out of `docker inspect`'s Env array (audit MUL-172, F4).
package secrets

import (
	"os"
	"path/filepath"
	"strings"
)

// dockerSecretsDir is the standard mount point Docker (and Swarm/Compose v2
// `secrets:`) uses for file-backed secrets.
const dockerSecretsDir = "/run/secrets"

// FromFileOrEnv prefers the contents of /run/secrets/<lowercase(name)> when
// the file exists, otherwise falls back to os.Getenv(name). A single trailing
// newline is trimmed (Docker writes secret files without one, but operators
// often `echo "value" >` them). Returns "" when neither source is set so
// callers keep their existing zero-value behavior.
func FromFileOrEnv(name string) string {
	return fromDirOrEnv(dockerSecretsDir, name)
}

func fromDirOrEnv(dir, name string) string {
	path := filepath.Join(dir, strings.ToLower(name))
	if b, err := os.ReadFile(path); err == nil {
		return strings.TrimRight(string(b), "\n")
	}
	return os.Getenv(name)
}
