// Command hevy-mcp runs the Hevy MCP server, exposing the Hevy REST API as MCP
// tools.
//
// Authentication:
//   - In stdio mode the HEVY_API_KEY env var is required and is used for the
//     entire process lifetime (one process == one user).
//   - In HTTP mode each user authenticates via the OAuth 2.1 flow defined by
//     the MCP authorization spec. They paste their Hevy API key on the
//     consent page; the server wraps it in a signed+encrypted access token
//     which Claude presents as Authorization: Bearer on every MCP request.
//
// Runtime options (non-secret) are passed as CLI flags:
//
//	--transport stdio|http       transport selection (default stdio)
//	--port 8080                  HTTP listen port (only used when --transport=http)
//	--base-url URL               override Hevy API base URL (testing only)
//	--issuer URL                 OAuth issuer + MCP canonical URL (required in HTTP mode;
//	                             falls back to the OAUTH_ISSUER env var if unset)
//
// HTTP mode env vars:
//
//	OAUTH_SIGNING_KEY            base64-encoded 32-byte HMAC-SHA256 key (required)
//	OAUTH_ENCRYPTION_KEY         base64-encoded 32-byte AES-256-GCM key (required)
//	OAUTH_ISSUER                 fallback for --issuer when the flag is unset
package main

import (
	"context"
	"encoding/base64"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/mark3labs/mcp-go/server"

	"github.com/yerden/hevy-mcp/internal/hevy"
	"github.com/yerden/hevy-mcp/internal/oauth"
	"github.com/yerden/hevy-mcp/internal/tools"
)

const (
	serverName    = "hevy-mcp"
	serverVersion = "0.1.0"
	mcpPath       = "/mcp"
)

func main() {
	var (
		transport = flag.String("transport", "stdio", "transport: stdio or http")
		port      = flag.Int("port", 8080, "HTTP listen port (only used with --transport=http)")
		baseURL   = flag.String("base-url", "", "override Hevy API base URL (testing only)")
		issuer    = flag.String("issuer", "", "canonical https URL of this server (required in HTTP mode; e.g. https://hevy.fly.dev)")
	)
	flag.Parse()

	if *transport != "stdio" && *transport != "http" {
		fmt.Fprintf(os.Stderr, "invalid --transport %q: must be stdio or http\n", *transport)
		os.Exit(2)
	}

	switch *transport {
	case "stdio":
		runStdio(*baseURL)
	case "http":
		iss := *issuer
		if iss == "" {
			iss = os.Getenv("OAUTH_ISSUER")
		}
		runHTTP(*port, *baseURL, iss)
	}
}

func runStdio(baseURL string) {
	envKey := os.Getenv("HEVY_API_KEY")
	if envKey == "" {
		log.Fatal("HEVY_API_KEY environment variable is required for stdio transport")
	}
	client := hevy.New(envKey, baseURL)
	s := newMCPServer(tools.StaticFactory(client))
	if err := server.ServeStdio(s); err != nil {
		log.Fatal(err)
	}
}

func runHTTP(port int, baseURL, issuer string) {
	if issuer == "" {
		log.Fatal("issuer is required in HTTP mode: set --issuer or OAUTH_ISSUER to the canonical https URL of this server")
	}
	signingKey, err := decodeSecret("OAUTH_SIGNING_KEY", 32)
	if err != nil {
		log.Fatal(err)
	}
	encryptionKey, err := decodeSecret("OAUTH_ENCRYPTION_KEY", 32)
	if err != nil {
		log.Fatal(err)
	}

	// Shared base client; OAuth bearer middleware injects the per-request
	// Hevy key into context, ContextFactory clones the base with that key.
	base := hevy.New("", baseURL)

	cfg := &oauth.Config{
		Issuer:        issuer,
		SigningKey:    signingKey,
		EncryptionKey: encryptionKey,
		ValidateHevyKey: func(ctx context.Context, hevyKey string) error {
			// Hit Hevy with the user-supplied key; if it returns user info,
			// the key is real. We don't care about the body.
			_, err := base.WithAPIKey(hevyKey).GetUserInfo()
			return err
		},
	}

	// Canonical resource URL per RFC 8707: the most specific URI the client
	// uses, which is the MCP endpoint itself (issuer + /mcp). This is what
	// Claude sends in the `resource` parameter and the `aud` claim.
	resourceURL := strings.TrimRight(issuer, "/") + mcpPath

	mcpSrv := newMCPServer(tools.ContextFactory(base))
	mcpHTTP := server.NewStreamableHTTPServer(mcpSrv)
	// Rate-limit per-user inside the bearer check so unauthenticated
	// traffic never touches the per-user buckets.
	guarded := cfg.BearerMiddleware(resourceURL, cfg.MCPRateLimit(mcpHTTP))

	mux := http.NewServeMux()
	mux.Handle(mcpPath, guarded)
	if err := cfg.Install(mux, resourceURL); err != nil {
		log.Fatal(err)
	}

	addr := ":" + strconv.Itoa(port)
	log.Printf("%s %s listening on %s (HTTP+OAuth); issuer=%s",
		serverName, serverVersion, addr, issuer)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatal(err)
	}
}

func newMCPServer(factory tools.ClientFactory) *server.MCPServer {
	s := server.NewMCPServer(serverName, serverVersion,
		server.WithToolCapabilities(true),
		server.WithRecovery(),
	)
	tools.RegisterAll(s, factory)
	return s
}

// decodeSecret reads a base64-encoded secret from the env var and verifies
// it decodes to exactly wantLen bytes. Standard base64 with or without
// padding is accepted; URL-safe alphabet works too.
func decodeSecret(envVar string, wantLen int) ([]byte, error) {
	raw := os.Getenv(envVar)
	if raw == "" {
		return nil, fmt.Errorf("%s environment variable is required", envVar)
	}
	var (
		decoded []byte
		err     error
	)
	for _, enc := range []*base64.Encoding{
		base64.StdEncoding,
		base64.RawStdEncoding,
		base64.URLEncoding,
		base64.RawURLEncoding,
	} {
		if decoded, err = enc.DecodeString(raw); err == nil {
			break
		}
	}
	if err != nil {
		return nil, fmt.Errorf("%s is not valid base64: %w", envVar, err)
	}
	if len(decoded) != wantLen {
		return nil, fmt.Errorf("%s decoded to %d bytes; want %d", envVar, len(decoded), wantLen)
	}
	return decoded, nil
}
