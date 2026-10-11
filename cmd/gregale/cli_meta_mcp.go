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
		cliFlag{Name: "tasks", Short: "accept modern MCP task handles for later inspection"},
		cliFlag{Name: "wait", Short: "wait for a task result; request cancellation on timeout"},
	)
	completionFlags := append([]cliFlag{}, remote[:4]...)
	completionFlags = append(completionFlags,
		cliFlag{Name: "legacy", Short: "use protocol 2025-11-25"},
		cliFlag{Name: "prompt", Short: "prompt name to complete", Value: "NAME"},
		cliFlag{Name: "resource-template", Short: "resource URI template to complete", Value: "URI-TEMPLATE"},
		cliFlag{Name: "argument", Short: "prompt argument or template variable name", Value: "NAME", Req: true},
		cliFlag{Name: "value", Short: "partial value; empty requests the first suggestions", Value: "TEXT", Req: true},
		cliFlag{Name: "context", Short: "previously resolved arguments as a JSON object", Value: "JSON"},
		cliFlag{Name: "context-file", Short: "read previous arguments from a JSON file", Value: "PATH"},
		cliFlag{Name: "timeout", Short: "total completion request timeout (default 30s)", Value: "DURATION"},
	)
	taskRemote := append(append([]cliFlag{}, remote...), cliFlag{Name: "task-id", Short: "opaque task ID (task-get, task-wait or task-cancel)", Value: "ID"})
	for i := range callFlags {
		if callFlags[i].Name == "timeout" {
			callFlags[i].Short = "total request timeout (default 30s; interactive/--wait 5m)"
		}
	}
	taskWaitFlags := append(append([]cliFlag{}, taskRemote...),
		cliFlag{Name: "interactive", Short: "answer modern MCP task input forms in the terminal"},
		cliFlag{Name: "input-responses-file", Short: "JSON file with elicitation responses keyed by request ID", Value: "PATH"},
	)
	for i := range taskWaitFlags {
		if taskWaitFlags[i].Name == "timeout" {
			taskWaitFlags[i].Short = "total task wait timeout (default 5m)"
		}
	}
	return cliCommand{Name: "mcp", DocSlug: "mcp", Short: "Scaffold, deploy and verify stateless MCP servers", Subcommands: []cliSub{
		{Name: "init", Short: "Create an MCP starter in Node, Go or Python", Flags: []cliFlag{
			{Name: "path", Short: "empty destination directory", Value: "DIR", Req: true},
			{Name: "runtime", Short: "starter language (node, go or python; default node)", Value: "RUNTIME", ClosedSet: []string{"node", "go", "python"}},
		}, Examples: []string{"gregale mcp init --path ./my-mcp", "gregale mcp init --path ./my-mcp --runtime go", "gregale mcp init --path ./my-mcp --runtime python"}},
		{Name: "deploy", Short: "Pin the candidate catalog to its revision, verify MCP discovery and promote", Flags: []cliFlag{{Name: "path", Short: "source directory (default .)", Value: "DIR"}, {Name: "name", Short: "app slug", Value: "SLUG", Req: true}, {Name: "profile", Short: "app resource profile", Value: "NAME", ClosedSet: []string{"micro", "small", "medium", "large", "xlarge"}}, {Name: "token-env", Short: "client token for external OAuth verification", Value: "ENV"}, {Name: "secrets-file", Short: "sealed app secrets", Value: "PATH"}, {Name: "release-policy", Short: "role-specific catalog baselines to verify before promotion", Value: "PATH"}, {Name: "mcp-task-migration-override", Short: "allow this deployment to replace incompatible MCP task workers once", Bool: true}, {Name: "timeout", Short: "deployment wait timeout in seconds (default 1200)", Value: "SECONDS"}}, Examples: []string{"gregale mcp deploy --path ./my-mcp --name my-mcp --profile small"}},
		{Name: "status", Short: "Qualify serving MCP replicas and watch hosting health or catalog changes", Flags: []cliFlag{
			{Name: "app", Short: "Gregale MCP app slug", Value: "SLUG", Req: true},
			{Name: "endpoint", Short: "literal MCP endpoint path (default /mcp)", Value: "PATH"},
			{Name: "token-env", Short: "MCP client bearer token environment variable", Value: "ENV"},
			{Name: "release-policy", Short: "role-specific catalog baselines and token environments", Value: "PATH"},
			{Name: "timeout", Short: "overall replica qualification deadline (default 10s)", Value: "DURATION"},
			{Name: "watch", Short: "poll until interrupted and report status changes", Bool: true},
			{Name: "interval", Short: "poll interval with --watch (default 30s)", Value: "DURATION"},
		}, Examples: []string{"gregale mcp status --app my-mcp", "gregale mcp status --app my-mcp --token-env MCP_READER_TOKEN", "gregale mcp status --app my-mcp --release-policy release.json --json", "gregale mcp status --app my-mcp --watch --interval 15s --json"}},
		{Name: "doctor", Short: "Check discovery, Origin rejection, compatibility and optional streaming", Flags: remote, Examples: []string{"gregale mcp doctor --app my-mcp --legacy --stream-tool stream_demo"}},
		{Name: "tools", Short: "Discover tool schemas without invoking tools", Flags: remote, Examples: []string{"gregale mcp tools --app my-mcp"}},
		{Name: "resources", Short: "Discover resource and template definitions without reading contents", Flags: remote, Examples: []string{"gregale mcp resources --app my-mcp"}},
		{Name: "resource-read", Short: "Read one explicitly selected resource URI", Flags: remote, Examples: []string{"gregale mcp resource-read --app my-mcp --uri 'file:///reports/current'"}},
		{Name: "resource-watch", Short: "Watch one resource URI for content updates", Flags: []cliFlag{
			{Name: "url", Short: "full MCP endpoint URL", Value: "URL"},
			{Name: "app", Short: "resolve a Gregale app's public endpoint", Value: "SLUG"},
			{Name: "endpoint", Short: "endpoint path with --app (default /mcp)", Value: "PATH"},
			{Name: "token-env", Short: "environment variable containing an MCP client token", Value: "ENV"},
			{Name: "uri", Short: "resource URI to read and watch", Value: "URI", Req: true},
			{Name: "interval", Short: "poll and stream reconciliation interval (default 30s)", Value: "DURATION"},
		}, Examples: []string{"gregale mcp resource-watch --app my-mcp --uri 'file:///reports/current'"}},
		{Name: "prompts", Short: "Discover prompt definitions without rendering them", Flags: remote, Examples: []string{"gregale mcp prompts --app my-mcp"}},
		{Name: "prompt-get", Short: "Render one explicitly selected prompt", Flags: remote, Examples: []string{`gregale mcp prompt-get --app my-mcp --prompt summarize --arguments '{"text":"weekly report"}'`}},
		{Name: "complete", Short: "Request bounded suggestions for a prompt or resource-template argument", Flags: completionFlags, Examples: []string{"gregale mcp complete --app my-mcp --prompt summarize --argument style --value exec", "gregale mcp complete --app my-mcp --resource-template 'customer://records/{recordId}' --argument recordId --value example-"}},
		{Name: "call", Short: "Execute one discovered tool; opt in to input requests or durable tasks", Flags: callFlags, Examples: []string{`gregale mcp call --app my-mcp --tool add --arguments '{"a":7,"b":5}'`, `gregale mcp call --app my-mcp --tool report_preview --wait`, `gregale mcp call --app my-mcp --tool report_preview --tasks`}},
		{Name: "task-get", Short: "Read the status or result of a previously returned task handle", Flags: taskRemote, Examples: []string{"gregale mcp task-get --app my-mcp --task-id 786512e2-9e0d-44bd-8f29-789f320fe840"}},
		{Name: "task-wait", Short: "Resume waiting for a task to finish", Flags: taskWaitFlags, Examples: []string{"gregale mcp task-wait --app my-mcp --task-id 786512e2-9e0d-44bd-8f29-789f320fe840", "gregale mcp task-wait --app my-mcp --task-id 786512e2-9e0d-44bd-8f29-789f320fe840 --interactive"}},
		{Name: "task-cancel", Short: "Request cooperative cancellation of a previously returned task", Flags: taskRemote, Examples: []string{"gregale mcp task-cancel --app my-mcp --task-id 786512e2-9e0d-44bd-8f29-789f320fe840"}},
		{Name: "tasks", Short: "Configure and inspect durable task worker scaling", Subcommands: []cliSub{
			{Name: "setup", Short: "Preview or apply the task-backlog scaling policy", Flags: []cliFlag{
				{Name: "app", Short: "worker app slug (defaults to the linked project app)", Value: "SLUG"},
				{Name: "min", Short: "minimum replicas (0 requires a ready observer app and fresh heartbeat)", Value: "N"},
				{Name: "max", Short: "maximum replicas (default 10)", Value: "N"},
				{Name: "target", Short: "outstanding tasks per worker (default 4)", Value: "N"},
				{Name: "observer-app", Short: "separate always-on worker app publishing task metrics", Value: "SLUG"},
				{Name: "apply", Short: "apply the proposed worker scaling policy", Bool: true},
			}, Examples: []string{"gregale mcp tasks setup --app mcp-worker", "gregale mcp tasks setup --app mcp-worker --observer-app mcp-observer --min 0 --apply"}},
			{Name: "status", Short: "Show task scaling policy, metric freshness and optional health checks", Flags: []cliFlag{
				{Name: "app", Short: "worker app slug (defaults to the linked project app)", Value: "SLUG"},
				{Name: "details", Short: "show waiting counts from the observer's latest handler-compatibility report", Bool: true},
				{Name: "max-oldest-age", Short: "fail when oldest queued task age exceeds this duration", Value: "DURATION"},
				{Name: "max-without-compatible-worker", Short: "fail when too many claimable tasks lack a compatible live worker", Value: "COUNT"},
				{Name: "require-fresh", Short: "fail when required MCP task metrics are missing or stale", Bool: true},
				{Name: "sustain-for", Short: "require an active violation to persist this long before failing", Value: "DURATION"},
				{Name: "poll-interval", Short: "poll interval while waiting for a sustained violation (default 15s)", Value: "DURATION"},
			}, Examples: []string{
				"gregale mcp tasks status --app mcp-worker --json",
				"gregale mcp tasks status --app mcp-worker --details",
				"gregale mcp tasks status --app mcp-worker --max-oldest-age 5m --require-fresh --sustain-for 2m",
				"gregale mcp tasks status --app mcp-worker --max-without-compatible-worker 0 --require-fresh",
			}},
			{Name: "observer", Short: "Set up and inspect a durable task metrics observer", Subcommands: []cliSub{
				{Name: "setup", Short: "Check observer config and ensure it keeps one worker replica", Flags: []cliFlag{
					{Name: "app", Short: "always-on observer worker app slug", Value: "SLUG", Req: true},
					{Name: "worker-app", Short: "task worker app slug whose metrics the observer publishes", Value: "SLUG", Req: true},
					{Name: "config", Short: "local MCP source config (default gregale-mcp.json)", Value: "PATH"},
					{Name: "apply", Short: "ensure the observer worker has at least one configured replica", Bool: true},
				}, Examples: []string{
					"gregale mcp tasks observer setup --app mcp-observer --worker-app mcp-worker",
					"gregale mcp tasks observer setup --app mcp-observer --worker-app mcp-worker --apply",
				}},
				{Name: "status", Short: "Check observer app readiness, credentials, namespace and heartbeat", Flags: []cliFlag{
					{Name: "app", Short: "observer worker app slug (defaults to the worker's linked observer)", Value: "SLUG"},
					{Name: "worker-app", Short: "task worker app slug whose metrics the observer publishes", Value: "SLUG", Req: true},
					{Name: "config", Short: "optional MCP source config for custom credential env names", Value: "PATH"},
				}, Examples: []string{
					"gregale mcp tasks observer status --worker-app mcp-worker",
					"gregale mcp tasks observer status --app mcp-observer --worker-app mcp-worker --config ./gregale-mcp.json",
				}},
			}},
		}},
		{Name: "watch", Short: "Watch caller-visible MCP catalog definitions for drift", Flags: []cliFlag{
			{Name: "url", Short: "full MCP endpoint URL", Value: "URL"},
			{Name: "app", Short: "resolve a Gregale app's public endpoint", Value: "SLUG"},
			{Name: "endpoint", Short: "endpoint path with --app (default /mcp)", Value: "PATH"},
			{Name: "token-env", Short: "environment variable containing an MCP client token", Value: "ENV"},
			{Name: "baseline", Short: "contract snapshot to watch for drift", Value: "PATH", Req: true},
			{Name: "interval", Short: "poll interval and stream reconciliation interval (default 30s)", Value: "DURATION"},
			{Name: "tools", Short: "subscribe to tool definition changes"},
			{Name: "resources", Short: "subscribe to resource and template definition changes"},
			{Name: "prompts", Short: "subscribe to prompt definition changes"},
		}, Examples: []string{"gregale mcp watch --app my-mcp --baseline gregale-mcp.lock.json"}},
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
