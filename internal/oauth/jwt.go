package oauth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// signedToken is a compact JWT-style token: base64url(headerJSON) "."
// base64url(payloadJSON) "." base64url(HMAC-SHA256(signing_input, key)). The
// header is fixed to {"alg":"HS256","typ":"JWT"} so it never needs to be
// inspected on verify — we just recompute the signature over the first two
// segments and compare.
//
// We use a custom payload type per token kind rather than a generic map so the
// claims are statically checkable.

const jwtHeaderHS256 = `{"alg":"HS256","typ":"JWT"}`

var jwtHeaderEncoded = base64.RawURLEncoding.EncodeToString([]byte(jwtHeaderHS256))

// authCodeClaims is the payload of a short-lived authorization code returned
// from /authorize and exchanged at /token. It carries enough state for the
// token endpoint to validate PKCE and redirect_uri without server-side
// session storage.
type authCodeClaims struct {
	Iss                 string `json:"iss"`
	Aud                 string `json:"aud"`
	Exp                 int64  `json:"exp"`
	Iat                 int64  `json:"iat"`
	RedirectURI         string `json:"redirect_uri"`
	CodeChallenge       string `json:"code_challenge"`
	CodeChallengeMethod string `json:"code_challenge_method"`
	Resource            string `json:"resource"`
	HevyKeyCiphertext   string `json:"hk_enc"` // base64url of AES-GCM(nonce||ciphertext||tag)
}

// accessTokenClaims is the payload of an access token presented in
// Authorization: Bearer ... on every MCP request.
type accessTokenClaims struct {
	Iss               string `json:"iss"`
	Aud               string `json:"aud"`
	Exp               int64  `json:"exp"`
	Iat               int64  `json:"iat"`
	HevyKeyCiphertext string `json:"hk_enc"`
}

// signJWT base64-encodes the JSON claims, appends the HS256 signature, and
// returns the three-segment token.
func signJWT(signingKey []byte, claims any) (string, error) {
	payloadJSON, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("oauth: marshal claims: %w", err)
	}
	payload := base64.RawURLEncoding.EncodeToString(payloadJSON)
	signingInput := jwtHeaderEncoded + "." + payload
	mac := hmac.New(sha256.New, signingKey)
	mac.Write([]byte(signingInput))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return signingInput + "." + sig, nil
}

// verifyJWT validates the signature and (if exp is set) the expiry, then
// decodes the payload into out.
func verifyJWT(signingKey []byte, token string, now time.Time, out any) error {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return errors.New("oauth: malformed token")
	}
	if parts[0] != jwtHeaderEncoded {
		return errors.New("oauth: unexpected token header")
	}
	signingInput := parts[0] + "." + parts[1]
	mac := hmac.New(sha256.New, signingKey)
	mac.Write([]byte(signingInput))
	expected := mac.Sum(nil)
	got, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return fmt.Errorf("oauth: decode signature: %w", err)
	}
	if !hmac.Equal(expected, got) {
		return errors.New("oauth: bad signature")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return fmt.Errorf("oauth: decode payload: %w", err)
	}
	if err := json.Unmarshal(payload, out); err != nil {
		return fmt.Errorf("oauth: decode claims: %w", err)
	}
	if e, ok := expFromClaims(payload); ok && now.Unix() >= e {
		return errors.New("oauth: token expired")
	}
	return nil
}

// expFromClaims peeks at the "exp" field without re-unmarshaling into the
// typed claims struct. It's only used by verifyJWT for the expiry check so
// callers don't have to re-implement that check for every token kind.
func expFromClaims(payload []byte) (int64, bool) {
	var probe struct {
		Exp int64 `json:"exp"`
	}
	if err := json.Unmarshal(payload, &probe); err != nil {
		return 0, false
	}
	return probe.Exp, probe.Exp != 0
}

// encryptHevyKey wraps the user's Hevy API key in AES-256-GCM with a random
// 12-byte nonce. The output is base64url(nonce||ciphertext||tag) so it can
// ride inside a JWT claim without further encoding gymnastics.
func encryptHevyKey(encryptionKey []byte, hevyKey string) (string, error) {
	block, err := aes.NewCipher(encryptionKey)
	if err != nil {
		return "", fmt.Errorf("oauth: aes cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("oauth: gcm: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("oauth: nonce: %w", err)
	}
	ct := gcm.Seal(nonce, nonce, []byte(hevyKey), nil)
	return base64.RawURLEncoding.EncodeToString(ct), nil
}

// decryptHevyKey reverses encryptHevyKey.
func decryptHevyKey(encryptionKey []byte, ciphertext string) (string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(ciphertext)
	if err != nil {
		return "", fmt.Errorf("oauth: decode ciphertext: %w", err)
	}
	block, err := aes.NewCipher(encryptionKey)
	if err != nil {
		return "", fmt.Errorf("oauth: aes cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("oauth: gcm: %w", err)
	}
	if len(raw) < gcm.NonceSize() {
		return "", errors.New("oauth: ciphertext too short")
	}
	nonce, body := raw[:gcm.NonceSize()], raw[gcm.NonceSize():]
	pt, err := gcm.Open(nil, nonce, body, nil)
	if err != nil {
		return "", fmt.Errorf("oauth: decrypt: %w", err)
	}
	return string(pt), nil
}
