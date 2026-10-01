package main

func mcpCLICommand() cliCommand {
	remote := []cliFlag{
		{Name: "url", Short: "full MCP endpoint URL", Value: "URL"},
		{Name: "app", Short: "resolve a Gregale app's public endpoint", Value: "SLUG"},
		{Name: "endpoint", Short: "endpoint path with --app (default /mcp)", Value: "PATH"},
		{Name: "token-env", Short: "environment variable containing an MCP client token", Value: "ENV"},
		{Name: "legacy", Short: "check legacy compatibility (doctor), or use protocol 2025-11-25"},
		{Name: "tool", Short: "tool to execute (call)", Value: "NAME"},
		{Name: "stream-tool", Short: "explicitly execute a tool to verify live progress (doctor)", Value: "NAME"},
		{Name: "arguments", Short: "tool arguments as a JSON object", Value: "JSON"},
		{Name: "arguments-file", Short: "JSON file containing tool arguments", Value: "PATH"},
		{Name: "name", Short: "connection name (config)", Value: "NAME"},
		{Name: "timeout", Short: "total diagnostic timeout (default 30s)", Value: "DURATION"},
	}
	return cliCommand{Name: "mcp", DocSlug: "mcp", Short: "Scaffold, deploy and verify stateless MCP servers", Subcommands: []cliSub{
		{Name: "init", Short: "Create the Node MCP starter", Flags: []cliFlag{{Name: "path", Short: "empty destination directory", Value: "DIR", Req: true}}, Examples: []string{"gregale mcp init --path ./my-mcp"}},
		{Name: "deploy", Short: "Deploy the worktree, enable streaming and verify MCP discovery", Flags: []cliFlag{{Name: "path", Short: "source directory (default .)", Value: "DIR"}, {Name: "name", Short: "app slug", Value: "SLUG", Req: true}, {Name: "profile", Short: "app resource profile", Value: "NAME", ClosedSet: []string{"micro", "small", "medium", "large", "xlarge"}}, {Name: "token-env", Short: "client token for external OAuth verification", Value: "ENV"}, {Name: "secrets-file", Short: "sealed app secrets", Value: "PATH"}, {Name: "timeout", Short: "deployment wait timeout in seconds (default 1200)", Value: "SECONDS"}}, Examples: []string{"gregale mcp deploy --path ./my-mcp --name my-mcp --profile small"}},
		{Name: "doctor", Short: "Check discovery, Origin rejection, compatibility and optional streaming", Flags: remote, Examples: []string{"gregale mcp doctor --app my-mcp --legacy --stream-tool stream_demo"}},
		{Name: "tools", Short: "Discover tool schemas without invoking tools", Flags: remote, Examples: []string{"gregale mcp tools --app my-mcp"}},
		{Name: "call", Short: "Execute one discovered tool without automatic retries", Flags: remote, Examples: []string{`gregale mcp call --app my-mcp --tool add --arguments '{"a":7,"b":5}'`}},
		{Name: "config", Short: "Emit remote MCP connection JSON without credentials", Flags: remote, Examples: []string{"gregale mcp config --app my-mcp --name my-mcp"}},
	}}
}
