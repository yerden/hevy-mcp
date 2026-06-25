package tools

import (
	"context"
	"errors"

	"github.com/yerden/hevy-mcp/internal/hevy"
)

type apiKeyCtxKey struct{}

// WithAPIKey stashes a Hevy API key in ctx. In HTTP mode the OAuth bearer
// middleware sets this from the validated access token; in stdio mode it is
// never used (the static factory holds the key directly).
func WithAPIKey(ctx context.Context, key string) context.Context {
	return context.WithValue(ctx, apiKeyCtxKey{}, key)
}

// APIKeyFromContext returns the key stashed by WithAPIKey, if any.
func APIKeyFromContext(ctx context.Context) (string, bool) {
	v, ok := ctx.Value(apiKeyCtxKey{}).(string)
	return v, ok && v != ""
}

// ContextFactory builds a ClientFactory that reads the per-request Hevy API
// key out of context. It is the HTTP-mode counterpart to StaticFactory:
// every tool call expects the bearer middleware to have run first. Calls
// without a key in context fail with a clear error so the model surfaces a
// useful message rather than a transport error.
//
// base is reused — only the API key varies per request.
func ContextFactory(base *hevy.Client) ClientFactory {
	return func(ctx context.Context) (*hevy.Client, error) {
		key, ok := APIKeyFromContext(ctx)
		if !ok {
			return nil, errors.New("no Hevy API key in request context; OAuth bearer middleware must run first")
		}
		return base.WithAPIKey(key), nil
	}
}
