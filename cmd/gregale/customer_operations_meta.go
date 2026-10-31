package main

func customerOperationCLIManifest() []cliSub {
	common := []cliFlag{{Name: "app", Value: "SLUG", Short: "required in account mode; omit with --self"}, {Name: "timeout", Value: "D", Short: "local request/wait deadline; work continues"}}
	makeSub := func(name, short string, flags ...cliFlag) cliSub {
		all := append([]cliFlag{}, common...)
		all = append(all, flags...)
		switch name {
		case "get", "events", "watch", "download", "cancel":
			all = append(all, cliFlag{Name: "self", Short: "use authenticated tenant routes; omit --app"})
		}
		sub := cliSub{Name: name, Short: short, Flags: all}
		if name != "list" {
			sub.Positionals = []string{"<id>"}
		}
		return sub
	}
	generation := cliFlag{Name: "expected-generation", Value: "N", Short: "observed generation; stale decisions are rejected", Req: true}
	recoveryGeneration := generation
	recoveryGeneration.Req = false
	recoveryGeneration.Short = "observed generation for a new decision or preview; omit to resume"
	limit := cliFlag{Name: "limit", Value: "N", Short: "page size, 1–100"}
	after := cliFlag{Name: "after", Value: "N", Short: "event sequence or execution generation watermark"}
	return append(customerOperationDeveloperCLIManifest(), []cliSub{
		makeSub("list", "List one bounded page of retained business work", cliFlag{Name: "scope", Value: "SCOPE", Short: "explicit deployment environment", Req: true}, cliFlag{Name: "tenant", Value: "UUID", Short: "optional platform tenant filter"}, cliFlag{Name: "name", Value: "NAME", Short: "operation name"}, cliFlag{Name: "state", Value: "STATE", Short: "business state", ClosedSet: []string{"accepted", "running", "waiting", "succeeded", "failed", "cancelled", "requires_reconciliation"}}, limit, cliFlag{Name: "cursor", Value: "CURSOR", Short: "next page cursor"}),
		makeSub("get", "Read business result and independent delivery status"),
		makeSub("inspect", "Inspect steps, uncertain attempts, retained files and retry blockers"),
		makeSub("events", "Read durable event evidence and resync marker", after),
		makeSub("executions", "Read retained execution generations and attempt counts", after, limit),
		{Name: "timeline", Short: "Read one chronological history of events, attempts and deliveries", Positionals: []string{"<id>"}, Flags: []cliFlag{{Name: "app", Value: "SLUG", Short: "owned application; account mode only", Req: true}, common[1], limit, {Name: "cursor", Value: "CURSOR", Short: "next timeline page cursor"}, {Name: "all", Short: "follow cursors to the end"}, {Name: "logs", Short: "print archived instance log lines inside each HTTP attempt"}}},
		{Name: "milestones", Short: "Read business milestones for an operation or reference", Positionals: []string{"[<operation-id>]"}, Flags: []cliFlag{
			{Name: "app", Value: "SLUG", Short: "owned app slug, or app UUID with --self"},
			{Name: "self", Bool: true, Short: "derive customer ownership from credentials"},
			{Name: "scope", Value: "SCOPE", Short: "environment for a business-reference timeline"},
			{Name: "subject-type", Value: "TYPE", Short: "business reference type"},
			{Name: "subject-id", Value: "ID", Short: "exact business reference ID"},
			{Name: "workflow", Value: "NAME", Short: "workflow for one business workflow run"},
			{Name: "workflow-instance-id", Value: "ID", Short: "exact workflow run ID"},
			{Name: "workflow-state-cursor", Value: "CURSOR", Short: "next workflow state history page"},
			{Name: "stale-only", Bool: true, Short: "only runs past their declared state age threshold"},
			{Name: "tenant", Value: "UUID", Short: "account customer filter"},
			{Name: "limit", Value: "N", Short: "page size"},
			{Name: "cursor", Value: "CURSOR", Short: "next milestone page cursor"},
		}},
		withNotes(makeSub("watch", "Watch an operation's status changes", cliFlag{Name: "interval", Value: "D", Short: "polling interval, default 1s"}), "Exits 0 on success, 1 on failure or cancel, 4 on reconciliation, 124 on timeout and 130 when interrupted."),
		makeSub("download", "Publish verified artifact bytes to a new private file", cliFlag{Name: "artifact", Value: "UUID", Short: "artifact ID; optional only when one artifact exists"}, cliFlag{Name: "output", Value: "PATH", Short: "new output file", Req: true}),
		makeSub("cancel", "Request cancellation at an observed generation", generation),
		{Name: "signal", Short: "Deliver a named event to a waiting operation", Notes: []string{"Address the operation by ID or by business subject."}, Positionals: []string{"[<id>]"}, Flags: []cliFlag{{Name: "app", Value: "SLUG", Short: "owned application; omit with --self"}, common[1], {Name: "event", Value: "NAME", Short: "event the operation waits for", Req: true}, {Name: "signal-id", Value: "ID", Short: "stable idempotency key; repeat it to retry safely", Req: true}, {Name: "payload", Value: "JSON", Short: "JSON payload"}, {Name: "payload-file", Value: "PATH", Short: "file holding the JSON payload"}, {Name: "scope", Value: "SCOPE", Short: "environment, for a subject signal"}, {Name: "operation", Value: "NAME", Short: "operation name, for a subject signal"}, {Name: "subject", Value: "TYPE:ID", Short: "business subject, instead of an operation ID"}, {Name: "self", Short: "signal the authenticated tenant's own operations"}, {Name: "definition", Value: "UUID", Short: "operation definition, for a --self subject signal"}}},
		makeSub("approve", "Decide a declared approval on a waiting operation", generation, cliFlag{Name: "step", Value: "STEP", Short: "declared approval step", Req: true}, cliFlag{Name: "decision", Value: "DECISION", Short: "approval decision", Req: true, ClosedSet: []string{"approved", "rejected"}}, cliFlag{Name: "comment", Value: "TEXT", Short: "optional note recorded with the decision"}),
		makeSub("recover", "Preview or record an evidenced reconciliation decision", recoveryGeneration, cliFlag{Name: "receipt-file", Value: "PATH", Short: "private immutable decision request; omit selectors to resume"}, cliFlag{Name: "preview", Short: "read-only plan; exit 4 when blocked; omit decision ID and evidence"}, cliFlag{Name: "inspection-revision", Value: "REVISION", Short: "optional inspection revision checked when applying"}, cliFlag{Name: "recovery-id", Value: "ID", Short: "stable decision ID required when applying"}, cliFlag{Name: "resolution", Value: "RESOLUTION", Short: "explicit resolution for a new decision or preview; omit to resume", ClosedSet: []string{"succeeded", "failed", "cancelled", "safe_to_retry"}}, cliFlag{Name: "evidence-file", Value: "PATH", Short: "nonempty reconciliation evidence required when applying"}, cliFlag{Name: "result-file", Value: "PATH", Short: "JSON result required for succeeded"}),
		makeSub("delivery", "Inspect notification state, replay generation and receiver cooldown"),
		makeSub("delivery-attempts", "Read retained notification attempt evidence", limit, cliFlag{Name: "cursor", Value: "CURSOR", Short: "next attempt page cursor"}),
		withNotes(makeSub("retry-delivery", "Record a notification retry without repeating business work", cliFlag{Name: "delivery", Value: "UUID", Short: "observed dead delivery; omit selectors to resume"}, cliFlag{Name: "expected-replay-generation", Value: "N", Short: "observed notification generation; zero must be explicit"}, cliFlag{Name: "retry-id", Value: "ID", Short: "stable retry decision identity"}, cliFlag{Name: "receipt-file", Value: "PATH", Short: "private immutable request receipt", Req: true}), "The retry is backed by a receipt."),
	}...)
}

func customerOperationDeveloperCLIManifest() []cliSub {
	definitionFlags := []cliFlag{{Name: "app", Value: "SLUG", Short: "owned application", Req: true}, {Name: "deployment", Value: "UUID", Short: "immutable deployment ID", Req: true}, {Name: "timeout", Value: "D", Short: "local request deadline"}}
	return []cliSub{
		{Name: "outcomes", Short: "List completed workflows with explicit business outcomes", Flags: []cliFlag{{Name: "app", Value: "SLUG", Short: "owned app"}, {Name: "self", Short: "authenticated customer"}, {Name: "app-id", Value: "UUID", Short: "required with --self"}, {Name: "scope", Value: "SCOPE", Short: "explicit environment", Req: true}, {Name: "tenant", Value: "UUID", Short: "account customer filter"}, {Name: "workflow", Value: "NAME", Short: "workflow filter"}, {Name: "code", Value: "CODE", Short: "outcome code filter"}, {Name: "limit", Value: "N", Short: "page size 1–100"}, {Name: "cursor", Value: "CURSOR", Short: "continuation cursor"}}},
		{Name: "outcome-summary", Short: "Summarize explicit business outcomes", Flags: []cliFlag{{Name: "group-by", Value: "DIMENSION", Short: "summary grouping", ClosedSet: []string{"outcome", "workflow", "customer"}}, {Name: "app", Value: "SLUG", Short: "owned app"}, {Name: "self", Short: "authenticated customer"}, {Name: "app-id", Value: "UUID", Short: "required with --self"}, {Name: "scope", Value: "SCOPE", Short: "explicit environment", Req: true}, {Name: "tenant", Value: "UUID", Short: "account customer filter"}, {Name: "workflow", Value: "NAME", Short: "workflow filter"}, {Name: "code", Value: "CODE", Short: "outcome code filter"}, {Name: "limit", Value: "N", Short: "page size 1–100"}, {Name: "cursor", Value: "CURSOR", Short: "continuation cursor"}}},
		{Name: "attention", Short: "List workflows needing attention", Flags: []cliFlag{{Name: "app", Value: "SLUG", Short: "owned app in account mode"}, {Name: "self", Short: "authenticated customer queue"}, {Name: "app-id", Value: "UUID", Short: "required with --self"}, {Name: "scope", Value: "SCOPE", Short: "explicit environment", Req: true}, {Name: "tenant", Value: "UUID", Short: "account customer filter"}, {Name: "workflow", Value: "NAME", Short: "workflow filter"}, {Name: "target-operation", Value: "NAME", Short: "target Operation filter"}, {Name: "dependency-status", Value: "STATUS", Short: "unresolved prerequisite status", ClosedSet: []string{"waiting", "unknown", "outcome_mismatch"}}, {Name: "required-outcome-code", Value: "CODE", Short: "required prerequisite outcome"}, {Name: "priority", Value: "PRIORITY", Short: "application-assigned blocker urgency", ClosedSet: []string{"low", "normal", "high", "urgent"}}, {Name: "sort", Value: "ORDER", Short: "queue order; summaries retain group ordering", ClosedSet: []string{"updated_at", "deadline"}}, {Name: "owner", Value: "OWNER", Short: "exact application-assigned blocker owner; excludes --unassigned"}, {Name: "unassigned", Short: "only matching blockers without an owner; excludes --owner"}, {Name: "blocker-code", Value: "CODE", Short: "blocker code filter"}, {Name: "reason", Value: "REASON", Short: "attention reason", ClosedSet: []string{"blocked", "stale", "overdue", "dependency", "escalated", "unacknowledged", "follow_up_overdue", "awaiting_verification", "sla_breached", "sla_at_risk"}}, {Name: "limit", Value: "N", Short: "page size, 1–100"}, {Name: "cursor", Value: "CURSOR", Short: "continuation cursor"}}},
		{Name: "attention-summary", Short: "Summarize workflows needing attention", Flags: []cliFlag{{Name: "group-by", Value: "DIMENSION", Short: "summary grouping", ClosedSet: []string{"owner", "workflow", "blocker_code", "target_operation", "customer", "dependency_status", "required_outcome_code"}}, {Name: "app", Value: "SLUG", Short: "owned app in account mode"}, {Name: "self", Short: "authenticated customer queue"}, {Name: "app-id", Value: "UUID", Short: "required with --self"}, {Name: "scope", Value: "SCOPE", Short: "explicit environment", Req: true}, {Name: "tenant", Value: "UUID", Short: "account customer filter"}, {Name: "workflow", Value: "NAME", Short: "workflow filter"}, {Name: "target-operation", Value: "NAME", Short: "target Operation filter"}, {Name: "dependency-status", Value: "STATUS", Short: "unresolved prerequisite status", ClosedSet: []string{"waiting", "unknown", "outcome_mismatch"}}, {Name: "required-outcome-code", Value: "CODE", Short: "required prerequisite outcome"}, {Name: "priority", Value: "PRIORITY", Short: "application-assigned blocker urgency", ClosedSet: []string{"low", "normal", "high", "urgent"}}, {Name: "sort", Value: "ORDER", Short: "queue order; summaries retain group ordering", ClosedSet: []string{"updated_at", "deadline"}}, {Name: "owner", Value: "OWNER", Short: "exact application-assigned blocker owner; excludes --unassigned"}, {Name: "unassigned", Short: "only matching blockers without an owner; excludes --owner"}, {Name: "blocker-code", Value: "CODE", Short: "blocker code filter"}, {Name: "reason", Value: "REASON", Short: "attention reason", ClosedSet: []string{"blocked", "stale", "overdue", "dependency", "escalated", "unacknowledged", "follow_up_overdue", "awaiting_verification", "sla_breached", "sla_at_risk"}}, {Name: "limit", Value: "N", Short: "page size, 1–100"}, {Name: "cursor", Value: "CURSOR", Short: "continuation cursor"}}},
		{Name: "bindings", Short: "Generate typed application workflow bindings without credentials", Flags: []cliFlag{
			{Name: "app", Value: "SLUG", Short: "selected manifest app", Req: true},
			{Name: "plan", Value: "PLAN", Short: "explicit target plan", Req: true, ClosedSet: []string{"free", "hobby", "pro", "scale"}},
			{Name: "dir", Value: "PATH", Short: "source directory, default current directory"},
			{Name: "language", Value: "LANGUAGE", Short: "application language", Req: true, ClosedSet: []string{"typescript", "javascript", "go", "python"}},
			{Name: "output", Value: "PATH", Short: "generated module", Req: true},
			{Name: "package", Value: "NAME", Short: "Go package name, default workflowbindings"},
			{Name: "check", Short: "fail on stale bindings without writing"},
		}},
		{Name: "doctor", Short: "Observe submission blockers, delivery warnings and qualification gaps", Flags: []cliFlag{{Name: "app", Value: "SLUG", Short: "owned app", Req: true}, {Name: "deployment", Value: "UUID", Short: "exact deployment", Req: true}, {Name: "tenant", Value: "UUID", Short: "owned platform tenant", Req: true}, {Name: "name", Value: "NAME", Short: "optional operation name"}, {Name: "timeout", Value: "D", Short: "local diagnostic deadline"}}},
		{Name: "definitions", Short: "Discover deployed immutable contracts", Subcommands: []cliSub{
			{Name: "list", Short: "List ordered contract metadata", Flags: definitionFlags},
			{Name: "get", Short: "Read schemas and deployment pins as JSON", Positionals: []string{"<name>"}, Flags: definitionFlags},
		}},
		{Name: "validate", Short: "Validate source contracts and sample input without credentials", Notes: []string{"Sample input is optional."}, Flags: []cliFlag{{Name: "app", Value: "SLUG", Short: "selected manifest app", Req: true}, {Name: "plan", Value: "PLAN", Short: "explicit target plan", Req: true, ClosedSet: []string{"free", "hobby", "pro", "scale"}}, {Name: "dir", Value: "PATH", Short: "source directory, default current directory"}, {Name: "name", Value: "NAME", Short: "selected operation; required for a sample"}, {Name: "input-file", Value: "PATH", Short: "sample JSON input"}}},
		{Name: "types", Short: "Generate or check TypeScript input and output types", Flags: []cliFlag{{Name: "app", Value: "SLUG", Short: "selected manifest app", Req: true}, {Name: "plan", Value: "PLAN", Short: "explicit target plan", Req: true, ClosedSet: []string{"free", "hobby", "pro", "scale"}}, {Name: "dir", Value: "PATH", Short: "source directory, default current directory"}, {Name: "name", Value: "NAME", Short: "selected operation; defaults to all"}, {Name: "output", Value: "PATH", Short: "generated declaration path (default customer-operations.generated.d.ts)"}, {Name: "check", Short: "verify generated declarations are current without writing"}}},
		{Name: "start", Short: "Submit tenant-owned work with an immutable retry receipt", Flags: []cliFlag{{Name: "self", Short: "derive tenant from credentials", Req: true, Bool: true}, {Name: "definition", Value: "UUID", Short: "immutable definition ID for a new receipt"}, {Name: "input-file", Value: "PATH", Short: "JSON input for a new receipt"}, {Name: "idempotency-key", Value: "KEY", Short: "stable request identity for a new receipt"}, {Name: "receipt-file", Value: "PATH", Short: "private request receipt; existing files must match", Req: true}, {Name: "timeout", Value: "D", Short: "local request deadline; resume from the receipt"}}},
	}
}
