package main

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
	}
}

func standardInspectionCLIHelp(label string) []cliFlag {
	return []cliFlag{
		{Name: "org", Short: "organization slug", Value: "SLUG", Req: true},
		{Name: "id", Short: label, Value: "UUID", Req: true},
	}
}

func standardExceptionCLIHelp() []cliFlag {
	return []cliFlag{
		{Name: "org", Short: "organization slug", Value: "SLUG", Req: true},
		{Name: "app", Short: "application UUID", Value: "UUID", Req: true},
		{Name: "after", Short: "last exception UUID from the previous page", Value: "UUID"},
		{Name: "limit", Short: "page size (1..100)", Value: "N"},
	}
}
