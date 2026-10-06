package main

func mcpCLICommand() cliCommand {
	remote := []cliFlag{
		{Name: "url", Short: "full MCP endpoint URL", Value: "URL"},
		{Name: "app", Short: "resolve a Gregale app's public endpoint", Value: "SLUG"},
		{Name: "endpoint", Short: "endpoint path with --app (default /mcp)", Value: "PATH"},
		{Name: "token-env", Short: "environment variable containing an MCP client token", Value: "ENV"},
		{Name: "legacy", Short: "check legacy compatibility (doctor), or use protocol 2025-11-25"},
		{Name: "tool", Short: "tool to execute (call)", Value: "NAME"},
		{Name: "uri", Short: "resource URI to read (resource-read)", Value: "URI"},
		{Name: "prompt", Short: "prompt to render (prompt-get)", Value: "NAME"},
		{Name: "stream-tool", Short: "explicitly execute a tool to verify live progress (doctor)", Value: "NAME"},
		{Name: "arguments", Short: "tool or prompt arguments as a JSON object", Value: "JSON"},
		{Name: "arguments-file", Short: "JSON file containing tool or prompt arguments", Value: "PATH"},
		{Name: "name", Short: "connection name (config)", Value: "NAME"},
		{Name: "timeout", Short: "total diagnostic timeout (default 30s)", Value: "DURATION"},
	}
	callFlags := append(append([]cliFlag{}, remote...),
		cliFlag{Name: "interactive", Short: "answer modern MCP input forms in the terminal"},
		cliFlag{Name: "input-responses-file", Short: "JSON file with elicitation responses keyed by request ID", Value: "PATH"},
	)
	for i := range callFlags {
		if callFlags[i].Name == "timeout" {
			callFlags[i].Short = "total request timeout (default 30s; interactive 5m)"
		}
	}
	return cliCommand{Name: "mcp", DocSlug: "mcp", Short: "Scaffold, deploy and verify stateless MCP servers", Subcommands: []cliSub{
		{Name: "init", Short: "Create the Node MCP starter", Flags: []cliFlag{{Name: "path", Short: "empty destination directory", Value: "DIR", Req: true}}, Examples: []string{"gregale mcp init --path ./my-mcp"}},
		{Name: "deploy", Short: "Deploy the worktree, enable streaming and verify MCP discovery", Flags: []cliFlag{{Name: "path", Short: "source directory (default .)", Value: "DIR"}, {Name: "name", Short: "app slug", Value: "SLUG", Req: true}, {Name: "profile", Short: "app resource profile", Value: "NAME", ClosedSet: []string{"micro", "small", "medium", "large", "xlarge"}}, {Name: "token-env", Short: "client token for external OAuth verification", Value: "ENV"}, {Name: "secrets-file", Short: "sealed app secrets", Value: "PATH"}, {Name: "timeout", Short: "deployment wait timeout in seconds (default 1200)", Value: "SECONDS"}}, Examples: []string{"gregale mcp deploy --path ./my-mcp --name my-mcp --profile small"}},
		{Name: "doctor", Short: "Check discovery, Origin rejection, compatibility and optional streaming", Flags: remote, Examples: []string{"gregale mcp doctor --app my-mcp --legacy --stream-tool stream_demo"}},
		{Name: "tools", Short: "Discover tool schemas without invoking tools", Flags: remote, Examples: []string{"gregale mcp tools --app my-mcp"}},
		{Name: "resources", Short: "Discover resource and template definitions without reading contents", Flags: remote, Examples: []string{"gregale mcp resources --app my-mcp"}},
		{Name: "resource-read", Short: "Read one explicitly selected resource URI", Flags: remote, Examples: []string{"gregale mcp resource-read --app my-mcp --uri 'file:///reports/current'"}},
		{Name: "prompts", Short: "Discover prompt definitions without rendering them", Flags: remote, Examples: []string{"gregale mcp prompts --app my-mcp"}},
		{Name: "prompt-get", Short: "Render one explicitly selected prompt", Flags: remote, Examples: []string{`gregale mcp prompt-get --app my-mcp --prompt summarize --arguments '{"period":"week"}'`}},
		{Name: "call", Short: "Execute one discovered tool; resume input requests only when explicitly enabled", Flags: callFlags, Examples: []string{`gregale mcp call --app my-mcp --tool add --arguments '{"a":7,"b":5}'`, `gregale mcp call --app my-mcp --tool report_preview --interactive`}},
		{Name: "config", Short: "Emit remote MCP connection JSON without credentials", Flags: remote, Examples: []string{"gregale mcp config --app my-mcp --name my-mcp"}},
		{Name: "lock", Short: "Capture MCP catalog definitions without reading resources, rendering prompts or invoking tools", Flags: []cliFlag{
			{Name: "url", Short: "full MCP endpoint URL", Value: "URL"},
			{Name: "app", Short: "resolve a Gregale app endpoint", Value: "SLUG"},
			{Name: "endpoint", Short: "endpoint path with --app (default /mcp)", Value: "PATH"},
			{Name: "token-env", Short: "environment variable containing a client token", Value: "ENV"},
			{Name: "legacy", Short: "use protocol 2025-11-25"},
			{Name: "out", Short: "snapshot destination (default gregale-mcp.lock.json)", Value: "PATH"},
			{Name: "force", Short: "replace an existing regular snapshot file"},
			{Name: "timeout", Short: "total discovery timeout (default 30s)", Value: "DURATION"},
		}, Examples: []string{"gregale mcp lock --app my-mcp --out baseline.json"}},
		{Name: "diff", Short: "Compare local MCP catalogs and report changes needing review", Flags: []cliFlag{
			{Name: "before", Short: "baseline MCP contract snapshot", Value: "PATH", Req: true},
			{Name: "after", Short: "candidate MCP contract snapshot", Value: "PATH", Req: true},
			{Name: "check", Short: "fail on breaking changes or changes needing review"},
			{Name: "strict-catalog", Short: "require review when a caller gains visibility of any catalog definition"},
		}, Examples: []string{"gregale mcp diff --before baseline.json --after candidate.json --check --json"}},
	}}
}
