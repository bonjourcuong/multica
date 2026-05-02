package middleware

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"strings"

	"github.com/golang-jwt/jwt/v5"
	"github.com/multica-ai/multica/server/internal/auth"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// daemonAuthStrict reports whether the DAEMON_AUTH_STRICT env var is set to
// a truthy value. Default is false to preserve the legacy PAT/JWT-as-daemon
// fallback for installs that have not yet migrated to mdt_ tokens (MUL-195).
func daemonAuthStrict() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("DAEMON_AUTH_STRICT"))) {
	case "true", "1", "yes", "on":
		return true
	}
	return false
}

// daemonAuthFallbackAllowed reports whether a user-credential (mul_ PAT or
// JWT) may authenticate the given daemon route. Outside strict mode the
// fallback is permitted on every route. In strict mode only the
// daemon-bootstrap routes — /register, /heartbeat, and /workspaces/{id}/repos
// — accept a user credential; every other daemon route requires an mdt_
// token (MUL-195).
func daemonAuthFallbackAllowed(path string, strict bool) bool {
	if !strict {
		return true
	}
	switch path {
	case "/api/daemon/register", "/api/daemon/heartbeat":
		return true
	}
	const wsPrefix = "/api/daemon/workspaces/"
	const reposSuffix = "/repos"
	if strings.HasPrefix(path, wsPrefix) && strings.HasSuffix(path, reposSuffix) {
		mid := strings.TrimSuffix(strings.TrimPrefix(path, wsPrefix), reposSuffix)
		if mid != "" && !strings.Contains(mid, "/") {
			return true
		}
	}
	return false
}

// Daemon context keys.
type daemonContextKey int

const (
	ctxKeyDaemonWorkspaceID daemonContextKey = iota
	ctxKeyDaemonID
	ctxKeyDaemonUserID
)

// DaemonWorkspaceIDFromContext returns the workspace ID set by DaemonAuth middleware.
func DaemonWorkspaceIDFromContext(ctx context.Context) string {
	id, _ := ctx.Value(ctxKeyDaemonWorkspaceID).(string)
	return id
}

// DaemonIDFromContext returns the daemon ID set by DaemonAuth middleware.
func DaemonIDFromContext(ctx context.Context) string {
	id, _ := ctx.Value(ctxKeyDaemonID).(string)
	return id
}

// DaemonUserIDFromContext returns the user that minted the daemon token, set
// by DaemonAuth middleware for mdt_-prefixed tokens. Empty for the PAT/JWT
// fallback paths (those carry the user via X-User-ID instead).
func DaemonUserIDFromContext(ctx context.Context) string {
	id, _ := ctx.Value(ctxKeyDaemonUserID).(string)
	return id
}

// WithDaemonContext returns a new context with the daemon workspace ID,
// daemon ID, and the user that minted the token set. Used by tests to
// simulate the DaemonAuth middleware for mdt_ tokens.
func WithDaemonContext(ctx context.Context, workspaceID, daemonID, userID string) context.Context {
	ctx = context.WithValue(ctx, ctxKeyDaemonWorkspaceID, workspaceID)
	ctx = context.WithValue(ctx, ctxKeyDaemonID, daemonID)
	ctx = context.WithValue(ctx, ctxKeyDaemonUserID, userID)
	return ctx
}

// DaemonAuth validates daemon auth tokens (mdt_ prefix) or falls back to
// JWT/PAT validation for backward compatibility with daemons that
// authenticate via user tokens.
func DaemonAuth(queries *db.Queries) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authHeader := r.Header.Get("Authorization")
			if authHeader == "" {
				slog.Debug("daemon_auth: missing authorization header", "path", r.URL.Path)
				writeError(w, http.StatusUnauthorized, "missing authorization header")
				return
			}

			tokenString := strings.TrimPrefix(authHeader, "Bearer ")
			if tokenString == authHeader {
				slog.Debug("daemon_auth: invalid format", "path", r.URL.Path)
				writeError(w, http.StatusUnauthorized, "invalid authorization format")
				return
			}

			// Daemon token: "mdt_" prefix.
			if strings.HasPrefix(tokenString, "mdt_") {
				hash := auth.HashToken(tokenString)
				dt, err := queries.GetDaemonTokenByHash(r.Context(), hash)
				if err != nil {
					slog.Warn("daemon_auth: invalid daemon token", "path", r.URL.Path, "error", err)
					writeError(w, http.StatusUnauthorized, "invalid daemon token")
					return
				}

				ctx := context.WithValue(r.Context(), ctxKeyDaemonWorkspaceID, uuidToString(dt.WorkspaceID))
				ctx = context.WithValue(ctx, ctxKeyDaemonID, dt.DaemonID)
				ctx = context.WithValue(ctx, ctxKeyDaemonUserID, uuidToString(dt.UserID))
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}

			// MUL-195: when strict mode is on, user credentials (PAT or JWT)
			// may only authenticate daemon-bootstrap routes. Everything else
			// must use an mdt_ token. Default mode keeps the legacy
			// behaviour so existing daemon installs are not bricked at deploy
			// time; the cutover follows in a separate sub-issue.
			if !daemonAuthFallbackAllowed(r.URL.Path, daemonAuthStrict()) {
				slog.Warn("daemon_auth: user credential rejected on daemon route in strict mode", "path", r.URL.Path)
				writeError(w, http.StatusUnauthorized, "daemon token required for this route")
				return
			}

			// Fallback: PAT tokens ("mul_" prefix).
			if strings.HasPrefix(tokenString, "mul_") {
				hash := auth.HashToken(tokenString)
				pat, err := queries.GetPersonalAccessTokenByHash(r.Context(), hash)
				if err != nil {
					slog.Warn("daemon_auth: invalid PAT", "path", r.URL.Path, "error", err)
					writeError(w, http.StatusUnauthorized, "invalid token")
					return
				}
				r.Header.Set("X-User-ID", uuidToString(pat.UserID))
				go queries.UpdatePersonalAccessTokenLastUsed(context.Background(), pat.ID)
				next.ServeHTTP(w, r)
				return
			}

			// Fallback: JWT tokens.
			token, err := jwt.Parse(tokenString, func(token *jwt.Token) (any, error) {
				if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
					return nil, jwt.ErrSignatureInvalid
				}
				return auth.JWTSecret(), nil
			})
			if err != nil || !token.Valid {
				slog.Warn("daemon_auth: invalid token", "path", r.URL.Path, "error", err)
				writeError(w, http.StatusUnauthorized, "invalid token")
				return
			}

			claims, ok := token.Claims.(jwt.MapClaims)
			if !ok {
				writeError(w, http.StatusUnauthorized, "invalid claims")
				return
			}
			sub, ok := claims["sub"].(string)
			if !ok || strings.TrimSpace(sub) == "" {
				writeError(w, http.StatusUnauthorized, "invalid claims")
				return
			}
			r.Header.Set("X-User-ID", sub)
			next.ServeHTTP(w, r)
		})
	}
}
