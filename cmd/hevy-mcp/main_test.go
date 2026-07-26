package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseAllowedRedirectURIs(t *testing.T) {
	cases := map[string]struct {
		in   string
		want []string
	}{
		"empty":            {"", nil},
		"whitespace only":  {"   ,  ,\t", nil},
		"single":           {"https://claude.ai/api/mcp/auth_callback", []string{"https://claude.ai/api/mcp/auth_callback"}},
		"multi trimmed":    {" https://a/cb , https://b/cb ", []string{"https://a/cb", "https://b/cb"}},
		"drops empty gaps": {"https://a/cb,,https://b/cb,", []string{"https://a/cb", "https://b/cb"}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.want, parseAllowedRedirectURIs(tc.in))
		})
	}
}
