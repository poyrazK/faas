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
