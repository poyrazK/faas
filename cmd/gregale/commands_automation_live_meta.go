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
		{Name: "list", Short: "Read current target IDs and recent live verifications", Flags: flags()},
		{Name: "start", Short: "Explicitly start one saved case against real handlers", Flags: append(start, wait...)},
		{Name: "show", Short: "Read one verification without requiring a verdict", Flags: flags(run)},
		{Name: "check", Short: "Check a terminal verification and require a current passing verdict", Flags: flags(run)},
		{Name: "wait", Short: "Observe an existing run, check assertions and require a current passing verdict", Flags: append(flags(run), wait...)},
	}
}
