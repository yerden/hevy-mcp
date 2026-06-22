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
//
// Runtime options (non-secret) are passed as CLI flags:
//
//	--transport stdio|http   transport selection (default stdio)
//	--port 8080              HTTP listen port (only used when --transport=http)
//	--base-url URL           override Hevy API base URL (testing only)
package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"

	"github.com/mark3labs/mcp-go/server"

	"github.com/yerden/hevy-mcp/internal/hevy"
	"github.com/yerden/hevy-mcp/internal/tools"
)

const (
	serverName    = "hevy-mcp"
	serverVersion = "0.1.0"
)

func main() {
	var (
		transport = flag.String("transport", "stdio", "transport: stdio or http")
		port      = flag.Int("port", 8080, "HTTP listen port (only used with --transport=http)")
		baseURL   = flag.String("base-url", "", "override Hevy API base URL (testing only)")
	)
	flag.Parse()

	if *transport != "stdio" && *transport != "http" {
		fmt.Fprintf(os.Stderr, "invalid --transport %q: must be stdio or http\n", *transport)
		os.Exit(2)
	}

	envKey := os.Getenv("HEVY_API_KEY")

	// stdio mode requires the env key — there's nowhere else to get it from.
	if *transport == "stdio" && envKey == "" {
		log.Fatal("HEVY_API_KEY environment variable is required for stdio transport")
	}

	// Base client; the API key on this instance is just a placeholder — the
	// factory swaps it per call via WithAPIKey so the shared *http.Client (and
	// its connection pool) is reused.
	base := hevy.New(envKey, *baseURL)
	factory := tools.HeaderFactory(base, envKey)

	s := server.NewMCPServer(serverName, serverVersion,
		server.WithToolCapabilities(true),
		server.WithRecovery(),
	)
	tools.RegisterAll(s, factory)

	switch *transport {
	case "http":
		addr := ":" + strconv.Itoa(*port)
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
