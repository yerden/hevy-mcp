// Command hevy-mcp runs the Hevy MCP server, exposing the Hevy REST API as MCP
// tools.
//
// Authentication:
//   - In stdio mode the HEVY_API_KEY env var is required and is used for the
//     entire session (one process == one user).
//   - In HTTP mode the API key is normally provided per-session via the
//     X-Hevy-Api-Key request header, so one running server can be shared by
//     multiple Hevy accounts. HEVY_API_KEY remains a usable fallback if the
//     header is absent.
package main

import (
	"log"
	"net/http"
	"os"

	"github.com/mark3labs/mcp-go/server"

	"github.com/yerden/hevy-mcp/internal/hevy"
	"github.com/yerden/hevy-mcp/internal/tools"
)

const (
	serverName    = "hevy-mcp"
	serverVersion = "0.1.0"
)

func main() {
	envKey := os.Getenv("HEVY_API_KEY")
	baseURL := os.Getenv("HEVY_BASE_URL")
	transport := os.Getenv("MCP_TRANSPORT")

	// stdio mode requires the env key — there's nowhere else to get it from.
	if transport != "http" && envKey == "" {
		log.Fatal("HEVY_API_KEY environment variable is required for stdio transport")
	}

	// Base client; the API key on this instance is just a placeholder — the
	// factory swaps it per call via WithAPIKey so the shared *http.Client (and
	// its connection pool) is reused.
	base := hevy.New(envKey, baseURL)
	factory := tools.HeaderFactory(base, envKey)

	s := server.NewMCPServer(serverName, serverVersion,
		server.WithToolCapabilities(true),
		server.WithRecovery(),
	)
	tools.RegisterAll(s, factory)

	switch transport {
	case "http":
		port := os.Getenv("MCP_PORT")
		if port == "" {
			port = "8080"
		}
		addr := ":" + port
		h := server.NewStreamableHTTPServer(s,
			server.WithHTTPContextFunc(tools.HTTPHeaderInjector()),
		)
		log.Printf("%s %s listening on %s (HTTP streamable); auth via %s header%s",
			serverName, serverVersion, addr, tools.APIKeyHeader,
			fallbackNote(envKey),
		)
		if err := http.ListenAndServe(addr, h); err != nil {
			log.Fatal(err)
		}
	default:
		if err := server.ServeStdio(s); err != nil {
			log.Fatal(err)
		}
	}
}

func fallbackNote(envKey string) string {
	if envKey != "" {
		return " (HEVY_API_KEY env used as fallback)"
	}
	return ""
}
