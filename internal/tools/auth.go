package tools

import (
	"context"
	"errors"
	"net/http"

	"github.com/yerden/hevy-mcp/internal/hevy"
)

// APIKeyHeader is the HTTP header carrying a per-session Hevy API key.
const APIKeyHeader = "X-Hevy-Api-Key"

type apiKeyCtxKey struct{}

// WithAPIKey stashes a Hevy API key in ctx.
func WithAPIKey(ctx context.Context, key string) context.Context {
	return context.WithValue(ctx, apiKeyCtxKey{}, key)
}

// APIKeyFromContext returns the key stashed by WithAPIKey, if any.
func APIKeyFromContext(ctx context.Context) (string, bool) {
	v, ok := ctx.Value(apiKeyCtxKey{}).(string)
	return v, ok && v != ""
}

// HTTPHeaderInjector returns a function suitable for
// server.WithHTTPContextFunc that copies APIKeyHeader from each incoming
// request into ctx.
func HTTPHeaderInjector() func(ctx context.Context, r *http.Request) context.Context {
	return func(ctx context.Context, r *http.Request) context.Context {
		if key := r.Header.Get(APIKeyHeader); key != "" {
			ctx = WithAPIKey(ctx, key)
		}
		return ctx
	}
}

// HeaderFactory builds a ClientFactory that resolves the API key per request:
//   - First, look in the context (set by HTTPHeaderInjector).
//   - If absent, fall back to fallbackKey (typically the HEVY_API_KEY env var).
//   - If still empty, return an error so the tool call surfaces a clear message.
//
// base is reused (shared http.Client / base URL) — only the API key varies.
func HeaderFactory(base *hevy.Client, fallbackKey string) ClientFactory {
	return func(ctx context.Context) (*hevy.Client, error) {
		if key, ok := APIKeyFromContext(ctx); ok {
			return base.WithAPIKey(key), nil
		}
		if fallbackKey != "" {
			return base.WithAPIKey(fallbackKey), nil
		}
		return nil, errors.New("no Hevy API key: supply " + APIKeyHeader + " request header or set HEVY_API_KEY")
	}
}
