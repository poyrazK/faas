package main

func runDeliveriesCLISubcommand() cliSub {
	return cliSub{Name: "deliveries", Short: "Deliver Run outputs into app-owned storage", Subcommands: []cliSub{
		{Name: "deliver", Short: "Promote one declared output without rerunning code", Positionals: []string{"<app>", "<route>"}, Flags: []cliFlag{
			{Name: "run", Value: "UUID", Req: true}, {Name: "artifact", Value: "NAME", Req: true}, {Name: "content-type", Value: "TYPE"}, {Name: "idempotency-key", Value: "KEY", Req: true}, {Name: "wait", Bool: true}, {Name: "wait-timeout", Value: "DURATION"},
		}},
		{Name: "status", Short: "Read a durable delivery receipt", Positionals: []string{"<app>", "<route>", "<delivery-id>"}, Flags: []cliFlag{{Name: "wait", Bool: true}, {Name: "wait-timeout", Value: "DURATION"}}},
		{Name: "download-url", Short: "Issue a short-lived app-owned report download URL", Positionals: []string{"<app>", "<route>", "<delivery-id>"}},
	}}
}
