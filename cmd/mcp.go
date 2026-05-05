package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(mcpCmd)
}

// mcpCmd handles the universal "any MCP function" surface:
//
//	praxis mcp                       → help
//	praxis mcp list                  → catalog of MCP servers
//	praxis mcp <mcp>                 → list functions in <mcp>
//	praxis mcp <mcp> <fn> [--k v …]  → invoke
//
// The --arg val syntax for invoke is variadic and unknown to cobra, so this
// command disables flag parsing and routes manually based on positional
// arg count.
var mcpCmd = &cobra.Command{
	Use:   "mcp",
	Short: "Invoke any Praxis MCP function (universal verb)",
	Long: `Universal entry point for Praxis MCP tools.

  praxis mcp list                          list available MCP servers
  praxis mcp <mcp>                         list functions in an MCP server
  praxis mcp <mcp> <fn> [--key val …]      invoke a function

Examples:
  praxis mcp list
  praxis mcp cloud_cli
  praxis mcp cloud_cli list_cloud_integrations
  praxis mcp cloud_cli run_cloud_cli \
        --integration_name prod-aws \
        --command "ec2 describe-instances"
  praxis mcp k8s_cli kubectl_describe \
        --namespace prod --name api-x

Add --json on any form for machine-readable output. When stdout is not a
terminal (i.e. you're piping to another tool), JSON is emitted by default.`,
	DisableFlagParsing: true,
	Args:               cobra.ArbitraryArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		switch {
		case len(args) == 0:
			return cmd.Help()
		case args[0] == "list":
			notImplemented(3, "praxis mcp list (server catalog fetch)")
		case len(args) == 1:
			notImplemented(3, fmt.Sprintf("praxis mcp %s (function listing)", args[0]))
		default:
			notImplemented(3,
				fmt.Sprintf("praxis mcp %s %s (HTTP invoke)", args[0], args[1]))
		}
		return nil
	},
}
