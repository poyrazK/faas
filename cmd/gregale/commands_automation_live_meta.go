package main

func automationLiveCLISubs() []cliSub {
	common := []cliFlag{{Name: "app", Value: "SLUG", Req: true, Short: "app slug"}, {Name: "name", Value: "NAME", Req: true, Short: "automation name"}}
	run := cliFlag{Name: "run", Value: "UUID", Req: true, Short: "existing live verification run UUID"}
	wait := []cliFlag{{Name: "timeout", Value: "DURATION", Short: "maximum wait (default 5m, up to 1h)"}, {Name: "poll-interval", Value: "DURATION", Short: "poll interval (default 3s, 1s to 1m)"}}
	flags := func(extra ...cliFlag) []cliFlag { return append(append([]cliFlag{}, common...), extra...) }
	start := flags(
		cliFlag{Name: "case", Value: "NAME", Req: true, Short: "saved case with live expectations"},
		cliFlag{Name: "expected-version", Value: "N", Req: true, Short: "current saved automation version"},
		cliFlag{Name: "expected-published-version", Value: "N", Req: true, Short: "published automation revision"},
		cliFlag{Name: "expected-deployment-id", Value: "UUID", Req: true, Short: "live default deployment UUID"},
		cliFlag{Name: "idempotency-key", Value: "KEY", Req: true, Short: "stable key; retry only with the original start flags"},
		cliFlag{Name: "confirm-real-actions", Bool: true, Req: true, Short: "confirm this case can perform real actions"},
		cliFlag{Name: "wait", Bool: true, Short: "wait and require a current passing assertion verdict"})
	return []cliSub{
		{Name: "baseline", Short: "Manage named baselines and inspect audit history", Subcommands: automationLiveBaselineCLISubs()},
		{Name: "gate", Short: "Require passing suite evidence for a candidate target", Notes: []string{"Read-only."}, Flags: flags(
			cliFlag{Name: "policy", Value: "FILE", Short: "versioned required-case coverage policy JSON"},
			cliFlag{Name: "baseline", Value: "UUID", Short: "retained baseline suite UUID; exclusive with baseline-name"},
			cliFlag{Name: "baseline-name", Value: "NAME", Short: "resolve a named baseline; exclusive with baseline"},
			cliFlag{Name: "candidate", Value: "UUID", Req: true, Short: "distinct retained candidate suite UUID"},
			cliFlag{Name: "expected-version", Value: "N", Req: true, Short: "expected candidate saved automation version"},
			cliFlag{Name: "expected-published-version", Value: "N", Req: true, Short: "expected candidate published revision"},
			cliFlag{Name: "expected-deployment-id", Value: "UUID", Req: true, Short: "expected candidate live default deployment UUID"})},
		{Name: "suite", Short: "Run selected live cases sequentially with resumable private progress", Flags: automationLiveSuiteCLIFlags()},
		{Name: "list", Short: "Read current target IDs and recent live verifications", Flags: flags()},
		{Name: "start", Short: "Explicitly start one saved case against real handlers", Flags: append(start, wait...)},
		{Name: "show", Short: "Read one verification without requiring a verdict", Flags: flags(run)},
		{Name: "check", Short: "Check a terminal verification and require a current passing verdict", Flags: flags(run)},
		{Name: "wait", Short: "Observe a run, check assertions and require a passing verdict", Flags: append(flags(run), wait...)},
	}
}

func automationLiveSuiteCLIFlags() []cliFlag {
	return []cliFlag{
		{Name: "app", Value: "SLUG", Short: "app slug for a new suite"},
		{Name: "name", Value: "NAME", Short: "automation name for a new suite"},
		{Name: "case", Value: "NAME", Short: "explicit case selection; repeat in execution order, at most 32"},
		{Name: "expected-version", Value: "N", Short: "saved automation version for a new suite"},
		{Name: "expected-published-version", Value: "N", Short: "published revision for a new suite"},
		{Name: "expected-deployment-id", Value: "UUID", Short: "live default deployment UUID for a new suite"},
		{Name: "idempotency-key", Value: "KEY", Short: "stable suite key for a new suite"},
		{Name: "journal", Value: "PATH", Short: "new private progress file; required for a new suite"},
		{Name: "resume", Value: "PATH", Short: "continue the original selection from this suite journal"},
		{Name: "share", Bool: true, Short: "save a new suite in shared dashboard history; persisted for resume"},
		{Name: "confirm-real-actions", Bool: true, Req: true, Short: "confirm execution of remaining selected cases"},
		{Name: "timeout", Value: "DURATION", Short: "total invocation deadline (default 5m, up to 1h)"},
		{Name: "poll-interval", Value: "DURATION", Short: "poll interval (default 3s, 1s to 1m)"},
	}
}

func automationLiveBaselineCLISubs() []cliSub {
	common := []cliFlag{{Name: "app", Value: "SLUG", Req: true, Short: "app slug"}, {Name: "name", Value: "NAME", Req: true, Short: "automation name"}, {Name: "baseline-name", Value: "NAME", Req: true, Short: "named release track, such as production"}}
	promote := append(append([]cliFlag{}, common...), cliFlag{Name: "suite", Value: "UUID", Req: true, Short: "passing retained suite UUID"}, cliFlag{Name: "promotion-id", Value: "UUID", Req: true, Short: "stable promotion UUID; preserve for retries"}, cliFlag{Name: "expected-revision", Value: "N", Req: true, Short: "current baseline revision; explicit 0 creates"}, cliFlag{Name: "expected-version", Value: "N", Req: true, Short: "expected candidate saved version"}, cliFlag{Name: "expected-published-version", Value: "N", Req: true, Short: "expected published revision"}, cliFlag{Name: "expected-deployment-id", Value: "UUID", Req: true, Short: "expected candidate deployment UUID"}, cliFlag{Name: "policy", Value: "FILE", Req: true, Short: "version 2 coverage and age policy JSON"})
	retire := append(append([]cliFlag{}, common...), cliFlag{Name: "retirement-id", Value: "UUID", Req: true, Short: "stable retirement UUID; preserve for retries"}, cliFlag{Name: "expected-revision", Value: "N", Req: true, Short: "current positive baseline revision"}, cliFlag{Name: "confirm-retirement", Bool: true, Req: true, Short: "confirm permanent retirement; release slot and evidence pin"})
	return []cliSub{{Name: "retire", Short: "Permanently retire a baseline with audit history", Notes: []string{"Protected by revision checks."}, Flags: retire}, {Name: "list", Short: "Find active baselines and read retained evidence health", Notes: []string{"Never executes anything."}, Flags: common[:2]}, {Name: "show", Short: "Read the current immutable promotion receipt", Flags: common}, {Name: "history", Short: "Read up to 100 recent promotions", Flags: common}, {Name: "promote", Short: "Promote passing evidence to a baseline with revision checks", Notes: []string{"Never executes workflows."}, Flags: promote}}
}
