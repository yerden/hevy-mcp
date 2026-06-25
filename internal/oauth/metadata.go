package oauth

import (
	"encoding/json"
	"net/http"
)

// asMetadata is the JSON document returned at
// /.well-known/oauth-authorization-server per RFC 8414. We populate only the
// fields the MCP authorization spec requires plus a few commonly-expected
// ones; everything else is omitted.
type asMetadata struct {
	Issuer                            string   `json:"issuer"`
	AuthorizationEndpoint             string   `json:"authorization_endpoint"`
	TokenEndpoint                     string   `json:"token_endpoint"`
	ResponseTypesSupported            []string `json:"response_types_supported"`
	GrantTypesSupported               []string `json:"grant_types_supported"`
	CodeChallengeMethodsSupported     []string `json:"code_challenge_methods_supported"`
	TokenEndpointAuthMethodsSupported []string `json:"token_endpoint_auth_methods_supported"`
	ScopesSupported                   []string `json:"scopes_supported,omitempty"`
}

// newASMetadataHandler returns the /.well-known/oauth-authorization-server
// handler. The body is pre-marshaled at construction time so each request is
// just a write.
func (c *Config) newASMetadataHandler() http.Handler {
	meta := asMetadata{
		Issuer:                            c.Issuer,
		AuthorizationEndpoint:             c.Issuer + pathAuthorize,
		TokenEndpoint:                     c.Issuer + pathToken,
		ResponseTypesSupported:            []string{"code"},
		GrantTypesSupported:               []string{"authorization_code"},
		CodeChallengeMethodsSupported:     []string{allowedCodeChallengeMethod},
		TokenEndpointAuthMethodsSupported: []string{"none"}, // public client; PKCE provides security
	}
	body, _ := json.Marshal(meta)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, HEAD, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		switch r.Method {
		case http.MethodOptions:
			w.WriteHeader(http.StatusNoContent)
			return
		case http.MethodGet, http.MethodHead:
			// fall through
		default:
			w.Header().Set("Allow", "GET, HEAD, OPTIONS")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)
		if r.Method == http.MethodHead {
			return
		}
		_, _ = w.Write(body)
	})
}
