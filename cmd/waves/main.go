// Command waves is the Waves service, CLI, MCP bridge and guard hook in one binary.
package main

import (
	"context"
	"os"

	"waves/internal/cli"
	"waves/web"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "0.1.0-dev"

func main() {
	cli.Version = version
	cli.UI = web.UI()
	os.Exit(cli.Run(context.Background(), os.Args[1:], os.Stdout, os.Stderr))
}
