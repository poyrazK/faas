package main

func standardMutationCLIHelp(revoke bool) []cliFlag {
	flags := []cliFlag{
		{Name: "org", Short: "organization slug", Value: "SLUG", Req: true},
		{Name: "app", Short: "application UUID", Value: "UUID", Req: true},
		{Name: "file", Short: "complete mutation JSON including expected_revision", Value: "PATH", Req: true},
	}
	if revoke {
		flags = append(flags, cliFlag{Name: "id", Short: "exception UUID", Value: "UUID", Req: true})
	}
	return flags
}

func standardExceptionMutationCLIHelp() []cliSub {
	return []cliSub{
		{Name: "approve", Short: "Approve one field with a reason and expiry; admin only, release gated", Flags: standardMutationCLIHelp(false)},
		{Name: "revoke", Short: "Revoke an exception and retain its history; admin only, release gated", Flags: standardMutationCLIHelp(true)},
	}
}

func standardResourceCLIHelp() []cliSub {
	return []cliSub{
		{Name: "list", Short: "List organization resources", Flags: []cliFlag{
			{Name: "org", Short: "organization slug", Value: "SLUG", Req: true},
			{Name: "after", Short: "last resource UUID from the previous page", Value: "UUID"},
			{Name: "limit", Short: "page size (1..100)", Value: "N"},
		}},
		{Name: "show", Short: "Inspect an immutable resource; credentials are omitted", Flags: []cliFlag{
			{Name: "org", Short: "organization slug", Value: "SLUG", Req: true},
			{Name: "id", Short: "resource UUID", Value: "UUID", Req: true},
		}},
		{Name: "create", Short: "Create an immutable resource from JSON", Flags: []cliFlag{
			{Name: "org", Short: "organization slug", Value: "SLUG", Req: true},
			{Name: "file", Short: "resource JSON file; credentials are sealed server-side", Value: "PATH", Req: true},
		}},
	}
}

func standardReviewCLIHelp() []cliSub {
	return []cliSub{
		{Name: "preview", Short: "Save affected applications and blockers without activating a change", Flags: []cliFlag{
			{Name: "org", Short: "organization slug", Value: "SLUG", Req: true},
			{Name: "file", Short: "assignment review JSON file", Value: "PATH", Req: true},
		}},
		{Name: "show", Short: "Inspect a saved review and its expiry", Flags: standardInspectionCLIHelp("review UUID")},
		{Name: "approve", Short: "Approve the exact saved review; admin only, release gated", Flags: standardRolloutCLIHelp("review UUID", "approval_hash JSON file")},
	}
}

func standardInspectionCLIHelp(label string) []cliFlag {
	return []cliFlag{
		{Name: "org", Short: "organization slug", Value: "SLUG", Req: true},
		{Name: "id", Short: label, Value: "UUID", Req: true},
	}
}

func standardOperationCLIHelp() []cliSub {
	return []cliSub{
		{Name: "pause", Short: "Pause outstanding rollout targets; admin only, release gated", Flags: standardRolloutCLIHelp("operation UUID", "expected_updated_at JSON file")},
		{Name: "resume", Short: "Resume a paused rollout; admin only, release gated", Flags: standardRolloutCLIHelp("operation UUID", "expected_updated_at JSON file")},
		{Name: "abort", Short: "Stop outstanding targets and retain installed settings; admin only, release gated", Flags: standardRolloutCLIHelp("operation UUID", "expected_updated_at JSON file")},
	}
}

func standardRolloutCLIHelp(label, body string) []cliFlag {
	return append(standardInspectionCLIHelp(label), cliFlag{Name: "file", Short: body, Value: "PATH", Req: true})
}

func standardExceptionCLIHelp() []cliFlag {
	return []cliFlag{
		{Name: "org", Short: "organization slug", Value: "SLUG", Req: true},
		{Name: "app", Short: "application UUID", Value: "UUID", Req: true},
		{Name: "after", Short: "last exception UUID from the previous page", Value: "UUID"},
		{Name: "limit", Short: "page size (1..100)", Value: "N"},
	}
}
