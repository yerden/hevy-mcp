package oauth

import (
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// newAuthorizeHandler implements the /oauth/authorize endpoint.
//
// GET renders the consent page with the OAuth request parameters preserved as
// hidden fields. POST takes the user-supplied Hevy API key, validates it
// against the live Hevy API, encrypts it, packages it inside a short-lived
// signed authorization code, and redirects the user-agent back to the
// pre-registered Claude callback URL with code + state.
//
// All non-conformant requests (unknown response_type, wrong code_challenge
// method, mismatched redirect_uri, missing resource) are rejected before any
// user interaction so we never render a consent page for an invalid request.
func (c *Config) newAuthorizeHandler(resourceURL string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			c.serveAuthorizeGet(w, r, resourceURL, "")
		case http.MethodPost:
			c.serveAuthorizePost(w, r, resourceURL)
		default:
			w.Header().Set("Allow", "GET, POST")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
}

// authorizeRequest is the validated subset of the OAuth authorization
// request we care about. PKCE and the resource indicator are mandatory.
type authorizeRequest struct {
	ClientID            string
	RedirectURI         string
	State               string
	CodeChallenge       string
	CodeChallengeMethod string
	Resource            string
}

// parseAuthorizeRequest pulls the OAuth params from the URL (GET) or form
// (POST) and rejects anything malformed before we touch the user.
func parseAuthorizeRequest(r *http.Request, resourceURL string) (authorizeRequest, error) {
	values := r.URL.Query()
	if r.Method == http.MethodPost {
		if err := r.ParseForm(); err != nil {
			return authorizeRequest{}, &authorizeError{httpStatus: http.StatusBadRequest, msg: "bad form"}
		}
		values = r.PostForm
	}

	got := func(k string) string { return strings.TrimSpace(values.Get(k)) }
	req := authorizeRequest{
		ClientID:            got("client_id"),
		RedirectURI:         got("redirect_uri"),
		State:               got("state"),
		CodeChallenge:       got("code_challenge"),
		CodeChallengeMethod: got("code_challenge_method"),
		Resource:            got("resource"),
	}

	if respType := got("response_type"); respType != "code" {
		return req, &authorizeError{httpStatus: http.StatusBadRequest, msg: "unsupported response_type"}
	}
	if req.ClientID == "" {
		return req, &authorizeError{httpStatus: http.StatusBadRequest, msg: "missing client_id"}
	}
	if req.RedirectURI != allowedRedirectURI {
		return req, &authorizeError{httpStatus: http.StatusBadRequest, msg: "redirect_uri not allowed"}
	}
	if req.CodeChallenge == "" {
		return req, &authorizeError{httpStatus: http.StatusBadRequest, msg: "missing code_challenge"}
	}
	if req.CodeChallengeMethod != allowedCodeChallengeMethod {
		return req, &authorizeError{httpStatus: http.StatusBadRequest, msg: "code_challenge_method must be S256"}
	}
	if req.Resource != resourceURL {
		return req, &authorizeError{httpStatus: http.StatusBadRequest, msg: "resource does not match this server"}
	}
	return req, nil
}

type authorizeError struct {
	httpStatus int
	msg        string
}

func (e *authorizeError) Error() string { return e.msg }

func (c *Config) serveAuthorizeGet(w http.ResponseWriter, r *http.Request, resourceURL, errMsg string) {
	req, err := parseAuthorizeRequest(r, resourceURL)
	if err != nil {
		if ae, ok := err.(*authorizeError); ok {
			http.Error(w, ae.msg, ae.httpStatus)
			return
		}
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	renderConsent(w, consentData{
		Action:              pathAuthorize,
		ClientID:            req.ClientID,
		RedirectURI:         req.RedirectURI,
		State:               req.State,
		CodeChallenge:       req.CodeChallenge,
		CodeChallengeMethod: req.CodeChallengeMethod,
		Resource:            req.Resource,
		Error:               errMsg,
	})
}

func (c *Config) serveAuthorizePost(w http.ResponseWriter, r *http.Request, resourceURL string) {
	// Rate-limit before touching the form: each POST costs a live Hevy
	// round-trip via ValidateHevyKey, so unbounded submissions turn us
	// into a credential-tester proxy.
	ip := clientIP(r)
	if !c.authorizeLimiter.Allow(ip) {
		slog.Warn("authorize rate limited",
			"event", "rate_limit",
			"endpoint", pathAuthorize,
			"ip", ip,
		)
		w.Header().Set("Retry-After", strconv.Itoa(60/authorizeRatePerMin))
		http.Error(w, "too many attempts, please slow down", http.StatusTooManyRequests)
		return
	}

	req, err := parseAuthorizeRequest(r, resourceURL)
	if err != nil {
		if ae, ok := err.(*authorizeError); ok {
			http.Error(w, ae.msg, ae.httpStatus)
			return
		}
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	hevyKey := strings.TrimSpace(r.PostForm.Get("hevy_api_key"))
	if hevyKey == "" {
		// Re-render the form with an error rather than 400ing — this is a
		// user mistake, not a client bug.
		c.renderConsentWithError(w, req, "Please enter your Hevy API key.")
		return
	}

	if err := c.ValidateHevyKey(r.Context(), hevyKey); err != nil {
		c.renderConsentWithError(w, req, "Hevy rejected that API key. Double-check it and try again.")
		return
	}

	now := c.Now()
	hevyKeyEnc, err := encryptHevyKey(c.EncryptionKey, hevyKey)
	if err != nil {
		http.Error(w, "internal error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	code, err := signJWT(c.SigningKey, authCodeClaims{
		Iss:                 c.Issuer,
		Aud:                 c.Issuer + pathToken,
		Exp:                 now.Add(c.AuthCodeTTL).Unix(),
		Iat:                 now.Unix(),
		RedirectURI:         req.RedirectURI,
		CodeChallenge:       req.CodeChallenge,
		CodeChallengeMethod: req.CodeChallengeMethod,
		Resource:            req.Resource,
		HevyKeyCiphertext:   hevyKeyEnc,
	})
	if err != nil {
		http.Error(w, "internal error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	redirect, _ := url.Parse(req.RedirectURI)
	q := redirect.Query()
	q.Set("code", code)
	if req.State != "" {
		q.Set("state", req.State)
	}
	redirect.RawQuery = q.Encode()
	http.Redirect(w, r, redirect.String(), http.StatusFound)
}

// renderConsentWithError reshows the form with an error message. Same
// hidden fields are preserved so the user can fix their input and retry.
func (c *Config) renderConsentWithError(w http.ResponseWriter, req authorizeRequest, errMsg string) {
	renderConsent(w, consentData{
		Action:              pathAuthorize,
		ClientID:            req.ClientID,
		RedirectURI:         req.RedirectURI,
		State:               req.State,
		CodeChallenge:       req.CodeChallenge,
		CodeChallengeMethod: req.CodeChallengeMethod,
		Resource:            req.Resource,
		Error:               errMsg,
	})
}
