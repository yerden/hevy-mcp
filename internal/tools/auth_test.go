package tools

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yerden/hevy-mcp/internal/hevy"
)

func TestAPIKeyFromContext(t *testing.T) {
	ctx := WithAPIKey(context.Background(), "abc")
	got, ok := APIKeyFromContext(ctx)
	assert.True(t, ok)
	assert.Equal(t, "abc", got)

	_, ok = APIKeyFromContext(context.Background())
	assert.False(t, ok)
}

func TestHTTPHeaderInjector_PullsKey(t *testing.T) {
	r := httptest.NewRequest("POST", "/mcp", nil)
	r.Header.Set(APIKeyHeader, "session-key")
	ctx := HTTPHeaderInjector()(context.Background(), r)
	got, ok := APIKeyFromContext(ctx)
	require.True(t, ok)
	assert.Equal(t, "session-key", got)
}

func TestHTTPHeaderInjector_NoHeaderNoOp(t *testing.T) {
	r := httptest.NewRequest("POST", "/mcp", nil)
	ctx := HTTPHeaderInjector()(context.Background(), r)
	_, ok := APIKeyFromContext(ctx)
	assert.False(t, ok)
}

func TestHeaderFactory_UsesContextKey(t *testing.T) {
	base := hevy.New("ignored", "http://example.invalid")
	f := HeaderFactory(base, "fallback")
	ctx := WithAPIKey(context.Background(), "session-key")
	c, err := f(ctx)
	require.NoError(t, err)
	// We can't read the apiKey field directly, but the returned client should
	// not be the same pointer as base (it's a shallow copy).
	assert.NotSame(t, base, c)
}

func TestHeaderFactory_FallbackToEnv(t *testing.T) {
	base := hevy.New("ignored", "http://example.invalid")
	f := HeaderFactory(base, "env-key")
	c, err := f(context.Background())
	require.NoError(t, err)
	assert.NotNil(t, c)
}

func TestHeaderFactory_NoKeyError(t *testing.T) {
	base := hevy.New("", "http://example.invalid")
	f := HeaderFactory(base, "")
	_, err := f(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), APIKeyHeader)
}

// End-to-end: register tools with HeaderFactory, drive a tool call with the
// per-session key in context, and verify the upstream request carries that key.
func TestEndToEnd_PerSessionAPIKey(t *testing.T) {
	var seenKey string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenKey = r.Header.Get("api-key")
		w.WriteHeader(200)
		_, _ = io.WriteString(w, `{"data":{"id":"u","name":"x","url":"y"}}`)
	}))
	t.Cleanup(up.Close)

	base := hevy.New("", up.URL)
	factory := HeaderFactory(base, "") // no fallback — must come from context
	s := server.NewMCPServer("t", "0", server.WithToolCapabilities(true))
	RegisterAll(s, factory)

	msg, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params":  map[string]any{"name": "hevy_get_user_info"},
	})
	ctx := WithAPIKey(context.Background(), "user-a-key")
	resp := s.HandleMessage(ctx, msg)
	jr := resp.(mcp.JSONRPCResponse)
	res := jr.Result.(*mcp.CallToolResult)
	assert.False(t, res.IsError, "tool result: %+v", res)
	assert.Equal(t, "user-a-key", seenKey)
}

func TestEndToEnd_MissingAPIKeyToolError(t *testing.T) {
	base := hevy.New("", "http://example.invalid")
	factory := HeaderFactory(base, "") // no fallback
	s := server.NewMCPServer("t", "0", server.WithToolCapabilities(true))
	RegisterAll(s, factory)

	msg, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params":  map[string]any{"name": "hevy_get_user_info"},
	})
	resp := s.HandleMessage(context.Background(), msg)
	jr := resp.(mcp.JSONRPCResponse)
	res := jr.Result.(*mcp.CallToolResult)
	require.True(t, res.IsError)
	tc := res.Content[0].(mcp.TextContent)
	assert.Contains(t, tc.Text, APIKeyHeader)
}
