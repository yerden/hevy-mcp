package oauth

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
)

// newTokenHandler implements POST /oauth/token. The only supported grant is
// authorization_code with PKCE. On success it returns a JSON envelope with
// an access_token + token_type + expires_in.
func (c *Config) newTokenHandler(resourceURL string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST, OPTIONS")
			writeTokenError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		if err := r.ParseForm(); err != nil {
			writeTokenError(w, http.StatusBadRequest, "invalid_request", "could not parse form body")
			return
		}
		got := func(k string) string { return strings.TrimSpace(r.PostForm.Get(k)) }

		if got("grant_type") != "authorization_code" {
			writeTokenError(w, http.StatusBadRequest, "unsupported_grant_type", "only authorization_code is supported")
			return
		}
		code := got("code")
		verifier := got("code_verifier")
		redirectURI := got("redirect_uri")
		if code == "" || verifier == "" {
			writeTokenError(w, http.StatusBadRequest, "invalid_request", "missing code or code_verifier")
			return
		}

		var claims authCodeClaims
		if err := verifyJWT(c.SigningKey, code, c.Now(), &claims); err != nil {
			writeTokenError(w, http.StatusBadRequest, "invalid_grant", "code invalid or expired")
			return
		}
		if claims.RedirectURI != redirectURI {
			writeTokenError(w, http.StatusBadRequest, "invalid_grant", "redirect_uri mismatch")
			return
		}
		if !verifyPKCE(verifier, claims.CodeChallenge) {
			writeTokenError(w, http.StatusBadRequest, "invalid_grant", "PKCE verification failed")
			return
		}

		now := c.Now()
		accessToken, err := signJWT(c.SigningKey, accessTokenClaims{
			Iss:               c.Issuer,
			Aud:               resourceURL,
			Exp:               now.Add(c.AccessTokenTTL).Unix(),
			Iat:               now.Unix(),
			HevyKeyCiphertext: claims.HevyKeyCiphertext,
		})
		if err != nil {
			writeTokenError(w, http.StatusInternalServerError, "server_error", err.Error())
			return
		}

		resp := map[string]any{
			"access_token": accessToken,
			"token_type":   "Bearer",
			"expires_in":   int(c.AccessTokenTTL.Seconds()),
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(resp)
	})
}

// verifyPKCE checks that S256(verifier) == challenge (base64url, no padding).
// Returns false on any inconsistency.
func verifyPKCE(verifier, challenge string) bool {
	if verifier == "" || challenge == "" {
		return false
	}
	sum := sha256.Sum256([]byte(verifier))
	computed := base64.RawURLEncoding.EncodeToString(sum[:])
	return subtle.ConstantTimeCompare([]byte(computed), []byte(challenge)) == 1
}

// writeTokenError renders an RFC 6749-style OAuth error JSON.
func writeTokenError(w http.ResponseWriter, status int, code, description string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"error":             code,
		"error_description": description,
	})
}
