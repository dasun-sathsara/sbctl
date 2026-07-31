// Command sbctl switches sing-box client profiles and controls the privileged
// service that runs them.
//
// This file is deliberately minimal. All behaviour lives in internal/cli, which
// takes its dependencies by injection, so the command surface can be exercised
// in tests without a service, a terminal, or root.
package main

import (
	"os"

	"sbctl/internal/cli"
)

// version is overridden at link time with -X main.version=<v>. When it is not,
// cli falls back to the module build metadata.
var version = "dev"

func main() {
	os.Exit(cli.Main(version))
}
