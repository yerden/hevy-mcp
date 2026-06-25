package tools

import (
	"context"
	"testing"

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

func TestContextFactory_UsesContextKey(t *testing.T) {
	base := hevy.New("ignored", "http://example.invalid")
	f := ContextFactory(base)
	ctx := WithAPIKey(context.Background(), "session-key")
	c, err := f(ctx)
	require.NoError(t, err)
	// We can't read the apiKey field directly, but the returned client
	// should be a fresh shallow copy (not the same pointer as base).
	assert.NotSame(t, base, c)
}

func TestContextFactory_NoKeyError(t *testing.T) {
	base := hevy.New("", "http://example.invalid")
	f := ContextFactory(base)
	_, err := f(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no Hevy API key")
}
