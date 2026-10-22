package main

func entityVisibilityCLIManifest() []cliSub {
	flags := []cliFlag{
		{Name: "app", Value: "SLUG", Short: "owned application", Req: true},
		{Name: "namespace", Value: "NAME", Short: "exact entity namespace", Req: true},
		{Name: "key", Value: "KEY", Short: "exact entity key", Req: true},
		{Name: "environment", Value: "ENV", Short: "exact environment; omitted uses API default"},
		{Name: "tenant", Value: "UUID", Short: "owned platform tenant"},
		{Name: "timeout", Value: "DURATION", Short: "observation deadline, default 5m, maximum 1h"},
		{Name: "interval", Value: "DURATION", Short: "watch poll interval, default 5s, range 1s to 1m"},
	}
	return []cliSub{{Name: "inspect", Short: "Read metadata and recovery diagnostics without invoking an entity", Flags: flags}, {Name: "watch", Short: "Poll metadata for a bounded window, emitting changes only", Flags: flags}}
}
