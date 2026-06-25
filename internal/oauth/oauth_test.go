package oauth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yerden/hevy-mcp/internal/tools"
)

// fixed test secrets; never use real values in tests
var (
	testSigning    = []byte("0123456789abcdef0123456789abcdef")
	testEncryption = []byte("ABCDEFGHIJKLMNOPQRSTUVWXYZ012345")
	testIssuer     = "https://hevy.test"
)

func newTestConfig(t *testing.T, validator HevyKeyValidator) *Config {
	t.Helper()
	cfg := &Config{
		Issuer:          testIssuer,
		SigningKey:      testSigning,
		EncryptionKey:   testEncryption,
		ValidateHevyKey: validator,
		Now:             func() time.Time { return time.Unix(1_700_000_000, 0) },
	}
	cfg.finalize()
	return cfg
}

func TestConfig_Validate(t *testing.T) {
	cases := map[string]*Config{
		"missing issuer":     {SigningKey: testSigning, EncryptionKey: testEncryption, ValidateHevyKey: noopValidator},
		"short signing key":  {Issuer: testIssuer, SigningKey: []byte("short"), EncryptionKey: testEncryption, ValidateHevyKey: noopValidator},
		"bad encryption key": {Issuer: testIssuer, SigningKey: testSigning, EncryptionKey: []byte("17 bytes long....."), ValidateHevyKey: noopValidator},
		"missing validator":  {Issuer: testIssuer, SigningKey: testSigning, EncryptionKey: testEncryption},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			assert.Error(t, c.Validate(), "expected validation error")
		})
	}

	good := &Config{Issuer: testIssuer, SigningKey: testSigning, EncryptionKey: testEncryption, ValidateHevyKey: noopValidator}
	assert.NoError(t, good.Validate())
}

func TestJWT_RoundTrip(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	claims := accessTokenClaims{
		Iss: testIssuer,
		Aud: testIssuer,
		Exp: now.Add(time.Hour).Unix(),
		Iat: now.Unix(),
	}
	tok, err := signJWT(testSigning, claims)
	require.NoError(t, err)

	var got accessTokenClaims
	require.NoError(t, verifyJWT(testSigning, tok, now, &got))
	assert.Equal(t, claims, got)
}

func TestJWT_Expired(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	tok, err := signJWT(testSigning, accessTokenClaims{Exp: now.Add(-time.Second).Unix()})
	require.NoError(t, err)
	var got accessTokenClaims
	err = verifyJWT(testSigning, tok, now, &got)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expired")
}

func TestJWT_BadSignature(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	tok, err := signJWT(testSigning, accessTokenClaims{Exp: now.Add(time.Hour).Unix()})
	require.NoError(t, err)
	// flip a byte in the signature
	bad := tok[:len(tok)-1] + "X"
	var got accessTokenClaims
	err = verifyJWT(testSigning, bad, now, &got)
	require.Error(t, err)
}

func TestEncryptDecryptHevyKey(t *testing.T) {
	ct, err := encryptHevyKey(testEncryption, "hk_live_abc")
	require.NoError(t, err)
	assert.NotContains(t, ct, "hk_live_abc")
	pt, err := decryptHevyKey(testEncryption, ct)
	require.NoError(t, err)
	assert.Equal(t, "hk_live_abc", pt)
}

func TestPKCE_S256(t *testing.T) {
	verifier := "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	assert.True(t, verifyPKCE(verifier, challenge))
	assert.False(t, verifyPKCE(verifier, challenge+"x"))
	assert.False(t, verifyPKCE("", challenge))
	assert.False(t, verifyPKCE(verifier, ""))
}

func TestASMetadataHandler(t *testing.T) {
	cfg := newTestConfig(t, noopValidator)
	rr := httptest.NewRecorder()
	cfg.newASMetadataHandler().ServeHTTP(rr, httptest.NewRequest("GET", "/.well-known/oauth-authorization-server", nil))
	require.Equal(t, http.StatusOK, rr.Code)

	var meta asMetadata
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &meta))
	assert.Equal(t, testIssuer, meta.Issuer)
	assert.Equal(t, testIssuer+pathAuthorize, meta.AuthorizationEndpoint)
	assert.Equal(t, testIssuer+pathToken, meta.TokenEndpoint)
	assert.Equal(t, []string{"code"}, meta.ResponseTypesSupported)
	assert.Equal(t, []string{"authorization_code"}, meta.GrantTypesSupported)
	assert.Equal(t, []string{"S256"}, meta.CodeChallengeMethodsSupported)
}

func TestAuthorize_GET_RendersForm(t *testing.T) {
	cfg := newTestConfig(t, noopValidator)
	params := authorizeQuery(t, "challenge")
	req := httptest.NewRequest("GET", pathAuthorize+"?"+params.Encode(), nil)
	rr := httptest.NewRecorder()
	cfg.newAuthorizeHandler(testIssuer).ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)
	body := rr.Body.String()
	assert.Contains(t, body, "Hevy API key")
	assert.Contains(t, body, "challenge")
}

func TestAuthorize_GET_RejectsBadRedirect(t *testing.T) {
	cfg := newTestConfig(t, noopValidator)
	params := authorizeQuery(t, "challenge")
	params.Set("redirect_uri", "https://attacker.example/callback")
	req := httptest.NewRequest("GET", pathAuthorize+"?"+params.Encode(), nil)
	rr := httptest.NewRecorder()
	cfg.newAuthorizeHandler(testIssuer).ServeHTTP(rr, req)
	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestAuthorize_GET_RejectsPlainPKCE(t *testing.T) {
	cfg := newTestConfig(t, noopValidator)
	params := authorizeQuery(t, "challenge")
	params.Set("code_challenge_method", "plain")
	req := httptest.NewRequest("GET", pathAuthorize+"?"+params.Encode(), nil)
	rr := httptest.NewRecorder()
	cfg.newAuthorizeHandler(testIssuer).ServeHTTP(rr, req)
	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestAuthorize_POST_HappyPath_AndTokenExchange(t *testing.T) {
	cfg := newTestConfig(t, func(ctx context.Context, key string) error {
		if key == "hk_real" {
			return nil
		}
		return errors.New("bad key")
	})

	verifier := "test-verifier-test-verifier-test-verifier-43"
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])

	// POST /authorize with the real key — should redirect with code.
	form := authorizeForm(t, challenge)
	form.Set("hevy_api_key", "hk_real")
	req := httptest.NewRequest("POST", pathAuthorize, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	cfg.newAuthorizeHandler(testIssuer).ServeHTTP(rr, req)
	require.Equal(t, http.StatusFound, rr.Code, "expected redirect; body: %s", rr.Body.String())

	loc, err := url.Parse(rr.Header().Get("Location"))
	require.NoError(t, err)
	assert.Equal(t, allowedRedirectURI, loc.Scheme+"://"+loc.Host+loc.Path)
	code := loc.Query().Get("code")
	require.NotEmpty(t, code)
	assert.Equal(t, "xyz-state", loc.Query().Get("state"))

	// Exchange the code at /token.
	tokForm := url.Values{}
	tokForm.Set("grant_type", "authorization_code")
	tokForm.Set("code", code)
	tokForm.Set("code_verifier", verifier)
	tokForm.Set("redirect_uri", allowedRedirectURI)
	tokReq := httptest.NewRequest("POST", pathToken, strings.NewReader(tokForm.Encode()))
	tokReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	tokRR := httptest.NewRecorder()
	cfg.newTokenHandler(testIssuer).ServeHTTP(tokRR, tokReq)
	require.Equal(t, http.StatusOK, tokRR.Code, "body: %s", tokRR.Body.String())

	var resp map[string]any
	require.NoError(t, json.Unmarshal(tokRR.Body.Bytes(), &resp))
	access, _ := resp["access_token"].(string)
	require.NotEmpty(t, access)
	assert.Equal(t, "Bearer", resp["token_type"])

	// Use the access token through the bearer middleware. The downstream
	// handler should see the decrypted Hevy key in context.
	var seen string
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key, ok := tools.APIKeyFromContext(r.Context())
		if !ok {
			t.Fatal("no API key in context")
		}
		seen = key
		w.WriteHeader(204)
	})
	mwReq := httptest.NewRequest("POST", "/mcp", nil)
	mwReq.Header.Set("Authorization", "Bearer "+access)
	mwRR := httptest.NewRecorder()
	cfg.newBearerMiddleware(testIssuer, next).ServeHTTP(mwRR, mwReq)
	require.Equal(t, 204, mwRR.Code, "body: %s", mwRR.Body.String())
	assert.Equal(t, "hk_real", seen)
}

func TestAuthorize_POST_BadKey_ReshowsForm(t *testing.T) {
	cfg := newTestConfig(t, func(ctx context.Context, key string) error { return errors.New("nope") })
	form := authorizeForm(t, "challenge")
	form.Set("hevy_api_key", "wrong")
	req := httptest.NewRequest("POST", pathAuthorize, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	cfg.newAuthorizeHandler(testIssuer).ServeHTTP(rr, req)
	// We reshow the form on bad keys (user error, not a client bug).
	require.Equal(t, http.StatusOK, rr.Code)
	assert.Contains(t, rr.Body.String(), "Hevy rejected")
}

func TestToken_BadPKCE(t *testing.T) {
	cfg := newTestConfig(t, func(ctx context.Context, key string) error { return nil })
	verifier := "real-verifier-real-verifier-real-verifier"
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])

	form := authorizeForm(t, challenge)
	form.Set("hevy_api_key", "hk_real")
	req := httptest.NewRequest("POST", pathAuthorize, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	cfg.newAuthorizeHandler(testIssuer).ServeHTTP(rr, req)
	require.Equal(t, http.StatusFound, rr.Code)
	loc, _ := url.Parse(rr.Header().Get("Location"))
	code := loc.Query().Get("code")

	// Try to exchange with the wrong verifier.
	tokForm := url.Values{}
	tokForm.Set("grant_type", "authorization_code")
	tokForm.Set("code", code)
	tokForm.Set("code_verifier", "wrong-verifier-wrong-verifier-wrong-verifier")
	tokForm.Set("redirect_uri", allowedRedirectURI)
	tokReq := httptest.NewRequest("POST", pathToken, strings.NewReader(tokForm.Encode()))
	tokReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	tokRR := httptest.NewRecorder()
	cfg.newTokenHandler(testIssuer).ServeHTTP(tokRR, tokReq)
	assert.Equal(t, http.StatusBadRequest, tokRR.Code)
	body, _ := io.ReadAll(tokRR.Body)
	assert.Contains(t, string(body), "invalid_grant")
}

func TestBearerMiddleware_MissingAuth_401WithWWWAuthenticate(t *testing.T) {
	cfg := newTestConfig(t, noopValidator)
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("next should not be called")
	})
	req := httptest.NewRequest("POST", "/mcp", nil)
	rr := httptest.NewRecorder()
	cfg.newBearerMiddleware(testIssuer, next).ServeHTTP(rr, req)
	require.Equal(t, http.StatusUnauthorized, rr.Code)
	wa := rr.Header().Get("WWW-Authenticate")
	assert.Contains(t, wa, "Bearer")
	assert.Contains(t, wa, "resource_metadata=")
	assert.Contains(t, wa, pathProtectedResourceMeta)
}

func TestBearerMiddleware_AudienceMismatch(t *testing.T) {
	cfg := newTestConfig(t, noopValidator)
	now := cfg.Now()
	tok, _ := signJWT(testSigning, accessTokenClaims{
		Aud:               "https://other.example",
		Exp:               now.Add(time.Hour).Unix(),
		HevyKeyCiphertext: mustEncrypt(t, testEncryption, "anything"),
	})
	req := httptest.NewRequest("POST", "/mcp", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rr := httptest.NewRecorder()
	cfg.newBearerMiddleware(testIssuer, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("aud-mismatched token should not pass")
	})).ServeHTTP(rr, req)
	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}

// helpers ----------------------------------------------------------------

func noopValidator(context.Context, string) error { return nil }

func authorizeQuery(t *testing.T, challenge string) url.Values {
	t.Helper()
	v := url.Values{}
	v.Set("response_type", "code")
	v.Set("client_id", "hevy-mcp")
	v.Set("redirect_uri", allowedRedirectURI)
	v.Set("state", "xyz-state")
	v.Set("code_challenge", challenge)
	v.Set("code_challenge_method", "S256")
	v.Set("resource", testIssuer)
	return v
}

func authorizeForm(t *testing.T, challenge string) url.Values {
	t.Helper()
	v := authorizeQuery(t, challenge)
	return v
}

func mustEncrypt(t *testing.T, key []byte, plain string) string {
	t.Helper()
	ct, err := encryptHevyKey(key, plain)
	require.NoError(t, err)
	return ct
}
