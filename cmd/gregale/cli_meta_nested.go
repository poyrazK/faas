package main

import "github.com/onebox-faas/faas/pkg/api"

func accountWebhookCLISubcommand() cliSub {
	secret := []cliFlag{
		{Name: "secret", Short: "signing secret (prefer --from-stdin)", Value: "VALUE"},
		{Name: "from-stdin", Short: "read the signing secret from stdin; mutually exclusive with --secret"},
	}
	settings := []cliFlag{
		{Name: "retry-policy", Short: "delivery retry policy", Value: "POLICY", ClosedSet: webhookClosedVocab},
		{Name: "delivery-format", Short: "delivery envelope", Value: "FORMAT", ClosedSet: webhookDeliveryFormatVocab},
	}
	add := []cliFlag{
		{Name: "target-url", Short: "HTTPS receiver URL", Value: "URL", Req: true},
		{Name: "event", Short: "distinct account event (repeatable)", Value: "EVENT", Req: true, Repeatable: true, ClosedSet: api.AllowedAccountWebhookEvents},
	}
	add = append(add, secret...)
	add = append(add, settings...)
	update := []cliFlag{
		{Name: "target-url", Short: "replacement HTTPS receiver URL", Value: "URL"},
		{Name: "event", Short: "replacement account event filter (repeatable)", Value: "EVENT", Repeatable: true, ClosedSet: api.AllowedAccountWebhookEvents},
		{Name: "enable", Short: "enable the receiver; mutually exclusive with --disable"},
		{Name: "disable", Short: "disable the receiver; mutually exclusive with --enable"},
	}
	update = append(update, secret...)
	update = append(update, settings...)
	return cliSub{Name: "account", Short: "Manage one release receiver across all account apps", Subcommands: []cliSub{
		{Name: "list", Short: "List account release receivers"},
		{Name: "add", Short: "Create a receiver; omitted signing secret is generated and shown once", Flags: add},
		{Name: "info", Short: "Show one receiver with a masked signing secret", Positionals: []string{"<id>"}},
		{Name: "update", Short: "Update a receiver", Positionals: []string{"<id>"}, Flags: update},
		{Name: "rm", Short: "Delete a receiver", Positionals: []string{"<id>"}, Flags: []cliFlag{{Name: "yes", Short: "accepted for consistency; rm does not prompt", Bool: true}}},
		{Name: "deliveries", Short: "Page through receiver deliveries", Positionals: []string{"<id>"}, Flags: []cliFlag{
			{Name: "page-size", Short: "delivery page size (1..100, default 50)", Value: "N"},
			{Name: "page-token", Short: "opaque delivery cursor", Value: "CURSOR"},
		}},
		{Name: "retry", Short: "Retry one failed delivery", Positionals: []string{"<id>", "<delivery-id>"}},
		{Name: "rotate-secret", Short: "Replace the signing secret", Notes: []string{"Read it with --from-stdin, or pass --secret."}, Positionals: []string{"<id>"}, Flags: secret},
	}}
}

func alertPresetCLISubcommand() cliSub {
	return cliSub{Name: "preset", Short: "Browse or enable catalog alert presets", Subcommands: []cliSub{
		{Name: "list", Short: "List the global alert preset catalog"},
		{Name: "enable", Short: "Create an app alert from a preset or choose one interactively", Examples: []string{"gregale alert preset enable --app my-api --interactive"}, Positionals: []string{"[<preset-name>]"}, Flags: []cliFlag{
			{Name: "interactive", Short: "choose a preset, review its rule, and enter a hidden signing secret", Bool: true},
			{Name: "app", Short: "app slug (interactive mode can use linked app or picker)", Value: "slug"},
			{Name: "webhook-url", Short: "HTTPS webhook receiver URL (required unless interactive or --channel)", Value: "URL"},
			{Name: "channel", Short: "notification channel id to deliver to (repeatable)", Value: "CHANNEL_ID", Repeatable: true},
			{Name: "webhook-secret-stdin", Short: "read the webhook signing secret from stdin"},
			{Name: "webhook-secret", Short: "signing secret (prefer --webhook-secret-stdin)", Value: "VALUE"},
			{Name: "action", Short: "alert action (default webhook)", Value: "ACTION", ClosedSet: api.AllowedAlertRuleActions},
			{Name: "cooldown-minutes", Short: "cooldown override; 0 uses the preset default", Value: "N"},
			{Name: "enabled", Short: "rule is enabled by default; --enabled=false disables it"},
		}},
	}}
}

func appEgressCLISubcommand(name, short, value string) cliSub {
	return cliSub{Name: name, Short: short, Subcommands: []cliSub{
		{Name: "show", Short: "Show the current app egress policy"},
		{Name: "add", Short: "Add one allowed destination", Positionals: []string{value}},
		{Name: "remove", Short: "Remove one allowed destination", Positionals: []string{value}},
		{Name: "clear", Short: "Clear the configured egress policy"},
	}}
}

func appNetworkCLISubcommand() cliSub {
	return cliSub{Name: "network", Short: "Inspect networking or manage private-network attachments", Subcommands: []cliSub{
		{Name: "show", Short: "Show network configuration and captured upstream telemetry"},
		{Name: "doctor", Short: "Check observed network health without sending probe traffic"},
		{Name: "attach", Short: "Request a private-network attachment", Positionals: []string{"<network-id>"}, Flags: []cliFlag{
			{Name: "region", Short: "private network region", Value: "REGION", Req: true},
			{Name: "cidrs", Short: "comma-separated private network CIDRs", Value: "CIDRS", Req: true},
		}},
		{Name: "detach", Short: "Remove the private-network attachment"},
	}}
}

func debugRequestFilterCLIFlags() []cliFlag {
	return []cliFlag{
		{Name: "since", Short: "lookback window", Value: "DURATION"},
		{Name: "route", Short: "route filter: /path matches every method", Value: "PATH"},
		{Name: "deployment-id", Short: "deployment filter", Value: "UUID"},
		{Name: "status", Short: "exact HTTP status (100..599)", Value: "N"},
		{Name: "cold-boot", Short: "cold-start filter", Value: "BOOL", ClosedSet: []string{"true", "false"}},
		{Name: "consumer-id", Short: "consumer UUID or __anonymous__", Value: "ID"},
		{Name: "min-latency-ms", Short: "minimum latency bucket in milliseconds", Value: "N"},
	}
}

func debugRequestsCLISubcommand() cliSub {
	list := append(debugRequestFilterCLIFlags(),
		cliFlag{Name: "limit", Short: "maximum rows per page (1..200)", Value: "N"},
		cliFlag{Name: "cursor", Short: "opaque pagination cursor", Value: "CURSOR"},
		cliFlag{Name: "all", Short: "walk every retained page"})
	watch := append(debugRequestFilterCLIFlags(),
		cliFlag{Name: "limit", Short: "maximum rows per poll (1..200)", Value: "N"},
		cliFlag{Name: "interval", Short: "poll interval (250ms..1h)", Value: "DURATION"},
		cliFlag{Name: "once", Short: "poll once and exit"})
	inspect := append(debugRequestFilterCLIFlags(), cliFlag{Name: "latest", Short: "select the newest retained request matching the filters"})
	export := append(debugRequestFilterCLIFlags(),
		cliFlag{Name: "format", Short: "export format (default ndjson)", Value: "FORMAT", ClosedSet: []string{"ndjson", "csv"}},
		cliFlag{Name: "limit", Short: "maximum exported rows (1..10000)", Value: "N"},
		cliFlag{Name: "output", Short: "destination file; - writes stdout", Value: "PATH"})
	request := []string{"<slug>", "<request-id-or-row-id>"}
	return cliSub{Name: "requests", Short: "Per-request telemetry and root-cause synthesis", Subcommands: []cliSub{
		{Name: "list", Short: "List recent request telemetry", Positionals: []string{"<slug>"}, Flags: list},
		{Name: "export", Short: "Export metadata-only request telemetry", Positionals: []string{"<slug>"}, Flags: export},
		{Name: "watch", Short: "Watch new or changed telemetry rows", Positionals: []string{"<slug>"}, Flags: watch},
		{Name: "get", Short: "Show request metadata", Positionals: request},
		{Name: "show", Short: "Show the request timeline and evidence", Positionals: request},
		{Name: "evidence", Short: "Show request evidence and explanation", Positionals: request},
		{Name: "explain", Short: "Synthesize findings and next actions", Positionals: request},
		{Name: "trace", Short: "Show the linked span tree", Positionals: request},
		{Name: "inspect", Short: "Inspect one retained request, or the latest one", Positionals: []string{"<slug>", "[<request-id-or-row-id>]"}, Flags: inspect},
		{Name: "replay", Short: "Queue a request replay against a mirror target", Positionals: request, Flags: []cliFlag{
			{Name: "deployment-id", Short: "enabled mirror target deployment", Value: "UUID"},
			{Name: "deployment", Short: "alias for --deployment-id", Value: "UUID"},
			{Name: "wait", Short: "wait for a terminal replay state"},
			{Name: "timeout", Short: "maximum wait (1s..1h)", Value: "DURATION"},
			{Name: "interval", Short: "poll interval (250ms..1m)", Value: "DURATION"},
		}},
	}}
}

func appTCPListenerCLISubcommand(slugPositional bool) cliSub {
	command := cliSub{
		Name:  "tcp",
		Short: "Manage raw TCP listeners for an app",
		Subcommands: []cliSub{
			{Name: "list", Short: "List TCP listeners"},
			{Name: "add", Short: "Create a TCP listener", Flags: []cliFlag{
				{Name: "name", Short: "listener name", Value: "NAME", Req: true},
				{Name: "guest-port", Short: "workload TCP port", Value: "PORT", Req: true},
				{Name: "public-port", Short: "stable public TCP port (40000..49999)", Value: "PORT"},
				{Name: "tls-mode", Short: "TLS mode (default passthrough)", Value: "MODE", ClosedSet: []string{string(api.TCPListenerTLSPassthrough), string(api.TCPListenerTLSTerminate)}},
				{Name: "tls-hostname", Short: "verified app-owned hostname for TLS termination", Value: "HOST"},
			}},
			{Name: "tls", Short: "Update one listener's TLS policy", Positionals: []string{"<name>"}, FlagsAfterPositionals: true, Flags: []cliFlag{
				{Name: "tls-mode", Short: "required TLS mode", Value: "MODE", Req: true, ClosedSet: []string{string(api.TCPListenerTLSPassthrough), string(api.TCPListenerTLSTerminate)}},
				{Name: "tls-hostname", Short: "verified app-owned termination hostname", Value: "HOST"},
			}},
			{Name: "tls-status", Short: "Show certificate observations for a listener", Positionals: []string{"<name>"}},
			{Name: "enable", Short: "Enable one listener", Positionals: []string{"<name>"}},
			{Name: "disable", Short: "Disable one listener", Positionals: []string{"<name>"}},
			{Name: "rm", Short: "Delete one listener", Positionals: []string{"<name>"}},
			{Name: "delete", Short: "Alias for rm", Positionals: []string{"<name>"}},
		},
	}
	if slugPositional {
		command.Positionals = []string{"<slug>"}
		command.SubcommandsAfterPositionals = true
	}
	return command
}

func appUDPListenerCLISubcommand(slugPositional bool) cliSub {
	command := cliSub{
		Name:  "udp",
		Short: "Manage raw UDP listeners for an app",
		Subcommands: []cliSub{
			{Name: "list", Short: "List UDP listeners"},
			{Name: "add", Short: "Create a UDP listener", Flags: []cliFlag{
				{Name: "name", Short: "listener name", Value: "NAME", Req: true},
				{Name: "guest-port", Short: "workload UDP port", Value: "PORT", Req: true},
				{Name: "public-port", Short: "stable public UDP port", Value: "PORT"},
			}},
			{Name: "enable", Short: "Enable one listener", Positionals: []string{"<name>"}},
			{Name: "disable", Short: "Disable one listener", Positionals: []string{"<name>"}},
			{Name: "rm", Short: "Delete one listener", Positionals: []string{"<name>"}},
			{Name: "delete", Short: "Alias for rm", Positionals: []string{"<name>"}},
		},
	}
	if slugPositional {
		command.Positionals = []string{"<slug>"}
		command.SubcommandsAfterPositionals = true
	}
	return command
}

func runTasksCLISubcommand() cliSub {
	return cliSub{Name: "tasks", Short: "Author, publish and invoke immutable named Run tasks", Subcommands: []cliSub{
		{Name: "validate", Short: "Validate a manifest and optional fixture offline", Flags: []cliFlag{
			{Name: "manifest", Value: "PATH", Req: true}, {Name: "plan", Value: "PLAN"}, {Name: "input", Value: "JSON|@PATH"},
		}},
		{Name: "generate", Short: "Generate or check version-pinned SDK bindings offline", Flags: []cliFlag{
			{Name: "manifest", Value: "PATH", Req: true}, {Name: "plan", Value: "PLAN"},
			{Name: "language", Value: "typescript|python", Req: true}, {Name: "check", Value: "PATH"},
		}},
		{Name: "test", Short: "Run a draft manifest as a smoke test before publication", Flags: []cliFlag{
			{Name: "manifest", Value: "PATH", Req: true}, {Name: "input", Value: "JSON|@PATH"},
			{Name: "input-file", Value: "PATH=LOCAL_FILE", Repeatable: true}, {Name: "customer-ref", Value: "REF"},
			{Name: "idempotency-key", Value: "KEY", Req: true}, {Name: "wait-timeout", Value: "DURATION"}, {Name: "output-dir", Value: "PATH"},
		}},
		{Name: "publish", Short: "Publish an account-owned immutable version", Flags: []cliFlag{{Name: "manifest", Value: "PATH", Req: true}}},
		{Name: "list", Short: "List task metadata", Flags: []cliFlag{{Name: "cursor", Value: "CURSOR"}}},
		{Name: "get", Short: "Read metadata without retained code", Positionals: []string{"<name@vN>"}},
		{Name: "retire", Short: "Erase retained code and retire a version", Positionals: []string{"<name@vN>"}},
		{Name: "invoke", Short: "Invoke a task using key-scoped input and receipt", Positionals: []string{"<name@vN>"}, Flags: []cliFlag{{Name: "customer-ref", Value: "REF"}, {Name: "input", Value: "JSON|@PATH"}, {Name: "input-file-id", Value: "PATH=FILE_UUID", Repeatable: true}, {Name: "idempotency-key", Value: "KEY"}, {Name: "wait", Bool: true}, {Name: "wait-timeout", Value: "DURATION"}, {Name: "output-dir", Value: "PATH"}}},
	}}
}

func runControlsCLISubcommand() cliSub {
	return cliSub{Name: "controls", Short: "Manage customer budgets and authenticated attribution", Subcommands: []cliSub{
		{Name: "customer", Short: "Read or replace a customer budget", Positionals: []string{"<ref>"}, Flags: []cliFlag{{Name: "manifest", Value: "PATH"}}},
		{Name: "task", Short: "Read or replace a task budget across versions", Positionals: []string{"<name>"}, Flags: []cliFlag{{Name: "manifest", Value: "PATH"}}},
		{Name: "binding", Short: "Read or replace an API key or Agent endpoint binding", Positionals: []string{"<api_key|agent_endpoint>", "<id>"}, Flags: []cliFlag{{Name: "manifest", Value: "PATH"}}},
		{Name: "unbind", Short: "Disable a binding using its current revision", Positionals: []string{"<api_key|agent_endpoint>", "<id>"}, Flags: []cliFlag{{Name: "expected-revision", Value: "N", Req: true}}},
	}}
}
func runHistoryCLISubcommand() cliSub {
	return cliSub{Name: "history", Short: "Read payload-free invocation history", Flags: []cliFlag{
		{Name: "task-name", Value: "NAME"}, {Name: "task-version", Value: "N"}, {Name: "customer-ref", Value: "REF"}, {Name: "agent-ref", Value: "REF"}, {Name: "status", Value: "STATUS"}, {Name: "since", Value: "RFC3339"}, {Name: "until", Value: "RFC3339"}, {Name: "cursor", Value: "CURSOR"},
	}}
}
