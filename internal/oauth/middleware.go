package oauth

import (
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/yerden/hevy-mcp/internal/tools"
)

// newBearerMiddleware verifies the Authorization: Bearer ... header on every
// MCP request, decrypts the wrapped Hevy API key, and forwards the request
// with the key stashed in context. Anything else gets 401 + WWW-Authenticate
// per RFC 9728 §5.1 / MCP spec, so the client knows where to discover the
// authorization server.
func (c *Config) newBearerMiddleware(resourceURL string, next http.Handler) http.Handler {
	// The metadata document lives at the host-root well-known path, not
	// rooted at the resource (which may include a /mcp path component).
	resourceMetadataURL := strings.TrimRight(c.Issuer, "/") + pathProtectedResourceMeta
	wwwAuth := `Bearer resource_metadata="` + resourceMetadataURL + `"`

	unauthorized := func(w http.ResponseWriter, errCode string) {
		w.Header().Set("WWW-Authenticate", wwwAuth+`, error="`+errCode+`"`)
		http.Error(w, errCode, http.StatusUnauthorized)
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authz := r.Header.Get("Authorization")
		if authz == "" {
			unauthorized(w, "invalid_token")
			return
		}
		const prefix = "Bearer "
		if !strings.HasPrefix(authz, prefix) {
			unauthorized(w, "invalid_token")
			return
		}
		token := strings.TrimSpace(authz[len(prefix):])

		var claims accessTokenClaims
		if err := verifyJWT(c.SigningKey, token, c.Now(), &claims); err != nil {
			unauthorized(w, "invalid_token")
			return
		}
		// Audience binding: per RFC 8707, the token MUST be intended for
		// this resource. Reject mismatches to prevent token reuse across
		// services sharing the same authorization server.
		if claims.Aud != resourceURL {
			unauthorized(w, "invalid_token")
			return
		}
		hevyKey, err := decryptHevyKey(c.EncryptionKey, claims.HevyKeyCiphertext)
		if err != nil || hevyKey == "" {
			unauthorized(w, "invalid_token")
			return
		}
		ctx := tools.WithAPIKey(r.Context(), hevyKey)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// MCPRateLimit throttles requests per authenticated user. It reads the
// decrypted Hevy API key that BearerMiddleware placed in the request
// context, keys a token bucket by SHA-256(key)[:16] so we never store or
// log the raw credential, and 429s once the bucket is empty.
//
// MUST be composed inside BearerMiddleware: no key in context means no
// bucket, and the request falls through unlimited (defensive — should
// never happen in a correctly wired mux).
func (c *Config) MCPRateLimit(next http.Handler) http.Handler {
	c.finalize()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hevyKey, ok := tools.APIKeyFromContext(r.Context())
		if !ok || hevyKey == "" {
			next.ServeHTTP(w, r)
			return
		}
		sum := sha256.Sum256([]byte(hevyKey))
		subject := hex.EncodeToString(sum[:8])
		if !c.mcpLimiter.Allow(subject) {
			slog.Warn("mcp rate limited",
				"event", "rate_limit",
				"endpoint", "/mcp",
				"subject", subject,
			)
			w.Header().Set("Retry-After", strconv.Itoa(60/mcpRatePerMin+1))
			http.Error(w, "too many requests", http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}
