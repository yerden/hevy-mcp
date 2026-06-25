package oauth

import (
	"net/http"
	"strings"

	"github.com/yerden/hevy-mcp/internal/tools"
)

// newBearerMiddleware verifies the Authorization: Bearer ... header on every
// MCP request, decrypts the wrapped Hevy API key, and forwards the request
// with the key stashed in context. Anything else gets 401 + WWW-Authenticate
// per RFC 9728 §5.1 / MCP spec, so the client knows where to discover the
// authorization server.
func (c *Config) newBearerMiddleware(resourceURL string, next http.Handler) http.Handler {
	resourceMetadataURL := strings.TrimRight(resourceURL, "/") + pathProtectedResourceMeta
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
