// Package oauth implements the authorization-server half of the MCP OAuth
// 2.1 flow used by hevy-mcp when running in HTTP transport mode. The Hevy
// API key is the user-supplied credential entered on the consent page; tokens
// issued by this server wrap the key in AES-GCM and carry it in the
// Authorization: Bearer header on every MCP request.
//
// The flow is stateless: no database or session store. Two server-side
// secrets are required (a 32-byte HMAC signing key and a 32-byte AES-256
// encryption key). Both authorization codes and access tokens are
// self-contained JWTs.
package oauth

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"time"

	mcpserver "github.com/mark3labs/mcp-go/server"
)

// HevyKeyValidator is called during the /authorize POST to verify the Hevy
// API key the user pasted into the consent form is real (e.g. by hitting
// Hevy's /v1/user/info endpoint). It must return a non-nil error iff the key
// is invalid.
type HevyKeyValidator func(ctx context.Context, hevyKey string) error

// Config wires the OAuth handlers. Build it once at startup and call
// Install to register the endpoints on an http.ServeMux.
type Config struct {
	// Issuer is the canonical https URL of this server, used as the OAuth
	// issuer identifier and as the audience for issued tokens. It MUST match
	// the URL users paste into Claude as the MCP connector URL.
	Issuer string

	// SigningKey is the 32-byte HMAC-SHA256 key used to sign authorization
	// codes and access tokens. Rotating it invalidates all outstanding
	// tokens — users have to reconnect.
	SigningKey []byte

	// EncryptionKey is the 32-byte AES-256-GCM key used to wrap the Hevy
	// API key inside tokens. Treat with the same care as SigningKey; can be
	// the same bytes if you prefer one secret.
	EncryptionKey []byte

	// ValidateHevyKey is invoked during /authorize POST to confirm the user
	// pasted a working Hevy API key. Required.
	ValidateHevyKey HevyKeyValidator

	// AccessTokenTTL is how long an issued access token is valid. Defaults
	// to 30 days when unset.
	AccessTokenTTL time.Duration

	// AuthCodeTTL is how long an authorization code is valid. Defaults to
	// 5 minutes when unset.
	AuthCodeTTL time.Duration

	// Now is a clock override for tests. Defaults to time.Now.
	Now func() time.Time
}

// Validate returns an error if the Config is missing required fields or
// has malformed values.
func (c *Config) Validate() error {
	if c.Issuer == "" {
		return errors.New("oauth: Issuer is required")
	}
	if len(c.SigningKey) < 32 {
		return errors.New("oauth: SigningKey must be at least 32 bytes")
	}
	if l := len(c.EncryptionKey); l != 16 && l != 24 && l != 32 {
		return errors.New("oauth: EncryptionKey must be 16, 24, or 32 bytes (AES-128/192/256)")
	}
	if c.ValidateHevyKey == nil {
		return errors.New("oauth: ValidateHevyKey is required")
	}
	return nil
}

// finalize fills in defaults. Idempotent — calling it twice is harmless.
func (c *Config) finalize() {
	if c.AccessTokenTTL == 0 {
		c.AccessTokenTTL = 30 * 24 * time.Hour
	}
	if c.AuthCodeTTL == 0 {
		c.AuthCodeTTL = 5 * time.Minute
	}
	if c.Now == nil {
		c.Now = time.Now
	}
}

// Endpoint paths. Centralized so the metadata handlers and the mux mounts
// agree on URLs.
const (
	pathAuthServerMetadata     = "/.well-known/oauth-authorization-server"
	pathProtectedResourceMeta  = "/.well-known/oauth-protected-resource"
	pathAuthorize              = "/oauth/authorize"
	pathToken                  = "/oauth/token"
	allowedRedirectURI         = "https://claude.ai/api/mcp/auth_callback"
	allowedCodeChallengeMethod = "S256"
)

// Install registers the four OAuth endpoints (authorization-server metadata,
// protected-resource metadata, authorize, token) on the given mux. The
// protectedResourceURL parameter is the canonical URL of the MCP server
// itself, used as the resource identifier per RFC 8707 and as the audience
// for issued access tokens. In most deployments it equals Config.Issuer.
//
// Returns an error if the Config is invalid.
func (c *Config) Install(mux *http.ServeMux, protectedResourceURL string) error {
	if err := c.Validate(); err != nil {
		return err
	}
	c.finalize()
	if protectedResourceURL == "" {
		return errors.New("oauth: protectedResourceURL is required")
	}
	mux.Handle(pathAuthServerMetadata, c.newASMetadataHandler())
	mux.Handle(pathProtectedResourceMeta, mcpserver.NewProtectedResourceMetadataHandler(
		mcpserver.ProtectedResourceMetadataConfig{
			Resource:               protectedResourceURL,
			AuthorizationServers:   []string{c.Issuer},
			BearerMethodsSupported: []string{"header"},
		},
	))
	mux.Handle(pathAuthorize, c.newAuthorizeHandler(protectedResourceURL))
	mux.Handle(pathToken, c.newTokenHandler(protectedResourceURL))
	return nil
}

// BearerMiddleware wraps next so that requests must carry a valid
// Authorization: Bearer access token issued by this Config. On success the
// Hevy API key extracted from the token is stashed in the request context
// via tools.WithAPIKey (callers read it back with tools.APIKeyFromContext).
// On failure the response is 401 with a WWW-Authenticate header pointing at
// the protected-resource metadata document, per the MCP authorization spec.
//
// protectedResourceURL must equal the value passed to Install.
func (c *Config) BearerMiddleware(protectedResourceURL string, next http.Handler) http.Handler {
	c.finalize()
	return c.newBearerMiddleware(protectedResourceURL, next)
}

// consentTmpl is the HTML rendered for GET /authorize. We keep it inline
// because the deployment story is a single static binary — embedding a
// separate template file would add a moving part.
var consentTmpl = template.Must(template.New("consent").Parse(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>Connect Hevy</title>
<style>
body { font-family: -apple-system, system-ui, sans-serif; max-width: 28rem; margin: 4rem auto; padding: 0 1rem; color: #222; }
h1 { font-size: 1.25rem; margin-bottom: 0.5rem; }
p { color: #555; font-size: 0.95rem; line-height: 1.45; }
label { display: block; font-weight: 600; margin-top: 1.5rem; margin-bottom: 0.5rem; }
input[type=password] { width: 100%; padding: 0.6rem; font-size: 1rem; border: 1px solid #ccc; border-radius: 0.375rem; box-sizing: border-box; }
button { margin-top: 1.5rem; width: 100%; padding: 0.7rem; font-size: 1rem; font-weight: 600; background: #111; color: #fff; border: 0; border-radius: 0.375rem; cursor: pointer; }
button:hover { background: #333; }
.err { color: #b00020; margin-top: 1rem; font-size: 0.9rem; }
.hint { font-size: 0.85rem; color: #666; margin-top: 0.4rem; }
.hint a { color: #0366d6; }
</style>
</head>
<body>
<h1>Connect your Hevy account</h1>
<p>Claude is requesting access to your Hevy data. Paste your Hevy API key below to grant access.</p>
<form method="post" action="{{.Action}}">
  <input type="hidden" name="client_id" value="{{.ClientID}}">
  <input type="hidden" name="redirect_uri" value="{{.RedirectURI}}">
  <input type="hidden" name="state" value="{{.State}}">
  <input type="hidden" name="code_challenge" value="{{.CodeChallenge}}">
  <input type="hidden" name="code_challenge_method" value="{{.CodeChallengeMethod}}">
  <input type="hidden" name="resource" value="{{.Resource}}">
  <label for="hevy_api_key">Hevy API key</label>
  <input id="hevy_api_key" type="password" name="hevy_api_key" autocomplete="off" required>
  <div class="hint">Find it in the Hevy app under Settings → Developer.</div>
  {{if .Error}}<div class="err">{{.Error}}</div>{{end}}
  <button type="submit">Connect</button>
</form>
</body>
</html>`))

// renderConsent writes the consent page using consentTmpl. errMsg is rendered
// only when non-empty (e.g. after a failed Hevy key validation).
func renderConsent(w http.ResponseWriter, data consentData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := consentTmpl.Execute(w, data); err != nil {
		http.Error(w, fmt.Sprintf("render consent: %v", err), http.StatusInternalServerError)
	}
}

type consentData struct {
	Action              string
	ClientID            string
	RedirectURI         string
	State               string
	CodeChallenge       string
	CodeChallengeMethod string
	Resource            string
	Error               string
}
