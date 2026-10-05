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
	limit := cliFlag{Name: "limit", Value: "N", Short: "page size, 1–100"}
	after := cliFlag{Name: "after", Value: "N", Short: "event sequence or execution generation watermark"}
	return append(customerOperationDeveloperCLIManifest(), []cliSub{
		makeSub("list", "List one bounded page of retained business work", cliFlag{Name: "scope", Value: "SCOPE", Short: "explicit deployment environment", Req: true}, cliFlag{Name: "tenant", Value: "UUID", Short: "optional platform tenant filter"}, cliFlag{Name: "name", Value: "NAME", Short: "operation name"}, cliFlag{Name: "state", Value: "STATE", Short: "business state", ClosedSet: []string{"accepted", "running", "succeeded", "failed", "cancelled", "requires_reconciliation"}}, limit, cliFlag{Name: "cursor", Value: "CURSOR", Short: "next page cursor"}),
		makeSub("get", "Read business result and independent delivery status"),
		makeSub("events", "Read durable event evidence and resync marker", after),
		makeSub("executions", "Read retained execution generations and attempt counts", after, limit),
		makeSub("watch", "Watch changed status; exit 0 success, 1 failure/cancel, 4 reconciliation, 124 timeout, 130 interrupt", cliFlag{Name: "interval", Value: "D", Short: "polling interval, default 1s"}),
		makeSub("download", "Publish verified artifact bytes to a new private file", cliFlag{Name: "artifact", Value: "UUID", Short: "artifact ID; optional only when one artifact exists"}, cliFlag{Name: "output", Value: "PATH", Short: "new output file", Req: true}),
		makeSub("cancel", "Request cancellation at an observed generation", generation),
		makeSub("recover", "Record an evidenced reconciliation decision", generation, cliFlag{Name: "recovery-id", Value: "ID", Short: "stable decision ID for duplicate requests", Req: true}, cliFlag{Name: "resolution", Value: "RESOLUTION", Short: "explicit reconciliation result", Req: true, ClosedSet: []string{"succeeded", "failed", "cancelled", "safe_to_retry"}}, cliFlag{Name: "evidence-file", Value: "PATH", Short: "nonempty reconciliation evidence", Req: true}, cliFlag{Name: "result-file", Value: "PATH", Short: "JSON result required for succeeded"}),
		makeSub("delivery", "Inspect notification state, replay generation and receiver cooldown"),
		makeSub("delivery-attempts", "Read retained notification attempt evidence", limit, cliFlag{Name: "cursor", Value: "CURSOR", Short: "next attempt page cursor"}),
		makeSub("retry-delivery", "Record a receipt-backed notification retry without repeating business work", cliFlag{Name: "delivery", Value: "UUID", Short: "observed dead delivery; omit selectors to resume"}, cliFlag{Name: "expected-replay-generation", Value: "N", Short: "observed notification generation; zero must be explicit"}, cliFlag{Name: "retry-id", Value: "ID", Short: "stable retry decision identity"}, cliFlag{Name: "receipt-file", Value: "PATH", Short: "private immutable request receipt", Req: true}),
	}...)
}

func customerOperationDeveloperCLIManifest() []cliSub {
	definitionFlags := []cliFlag{{Name: "app", Value: "SLUG", Short: "owned application", Req: true}, {Name: "deployment", Value: "UUID", Short: "immutable deployment ID", Req: true}, {Name: "timeout", Value: "D", Short: "local request deadline"}}
	return []cliSub{
		{Name: "doctor", Short: "Observe submission blockers, delivery warnings and unverified qualification", Flags: []cliFlag{{Name: "app", Value: "SLUG", Short: "owned app", Req: true}, {Name: "deployment", Value: "UUID", Short: "exact deployment", Req: true}, {Name: "tenant", Value: "UUID", Short: "owned platform tenant", Req: true}, {Name: "name", Value: "NAME", Short: "optional operation name"}, {Name: "timeout", Value: "D", Short: "local diagnostic deadline"}}},
		{Name: "definitions", Short: "Discover deployed immutable contracts", Subcommands: []cliSub{
			{Name: "list", Short: "List ordered contract metadata", Flags: definitionFlags},
			{Name: "get", Short: "Read schemas and deployment pins as JSON", Positionals: []string{"<name>"}, Flags: definitionFlags},
		}},
		{Name: "validate", Short: "Validate source contracts and optional sample input without credentials", Flags: []cliFlag{{Name: "app", Value: "SLUG", Short: "selected manifest app", Req: true}, {Name: "plan", Value: "PLAN", Short: "explicit target plan", Req: true, ClosedSet: []string{"free", "hobby", "pro", "scale"}}, {Name: "dir", Value: "PATH", Short: "source directory, default current directory"}, {Name: "name", Value: "NAME", Short: "selected operation; required for a sample"}, {Name: "input-file", Value: "PATH", Short: "sample JSON input"}}},
		{Name: "start", Short: "Submit tenant-owned work with an immutable retry receipt", Flags: []cliFlag{{Name: "self", Short: "derive tenant from credentials", Req: true}, {Name: "definition", Value: "UUID", Short: "immutable definition ID for a new receipt"}, {Name: "input-file", Value: "PATH", Short: "JSON input for a new receipt"}, {Name: "idempotency-key", Value: "KEY", Short: "stable request identity for a new receipt"}, {Name: "receipt-file", Value: "PATH", Short: "private request receipt; existing files must match", Req: true}, {Name: "timeout", Value: "D", Short: "local request deadline; resume from the receipt"}}},
	}
}
