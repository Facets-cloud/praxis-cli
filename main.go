package main

import (
	"context"
	"os"

	"github.com/Facets-cloud/praxis-cli/cmd"
	"github.com/Facets-cloud/praxis-cli/internal/agent"
)

func main() {
	// The agent runtime re-execs this binary as its process-sandbox child, using
	// its own bare flag dialect (`praxis -provider … -sandbox-child`) that cobra
	// cannot parse. Route that one invocation straight to the runtime, before the
	// command tree sees it; every human invocation is unaffected.
	if args, ok := agent.NativeDialectArgs(os.Args); ok {
		os.Exit(agent.RunNative(context.Background(), args))
	}
	cmd.Execute()
}
