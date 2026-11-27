package main

// insightsMetaCommon is the flags every `gregale insights` query takes.
func insightsMetaCommon() []cliFlag {
	return []cliFlag{
		{Name: "project", Short: "project slug", Value: "slug", Req: true},
		{Name: "environment", Short: "named environment (default production)", Value: "slug"},
		{Name: "from", Short: "RFC 3339 time or duration ago such as 14d (default 7 days, 28 for retention)", Value: "TIME"},
		{Name: "until", Short: "RFC 3339 time or duration ago (default now)", Value: "TIME"},
		{Name: "unit", Short: "customer (default) or subject", Value: "UNIT"},
		{Name: "by", Short: "breakdown: flag:KEY or property:KEY", Value: "BREAKDOWN"},
	}
}

// insightCohortMetaScope is the project scope of `insight cohort` commands.
func insightCohortMetaScope() []cliFlag {
	return []cliFlag{
		{Name: "project", Short: "project slug", Value: "slug", Req: true},
		{Name: "environment", Short: "named environment (default production)", Value: "slug"},
	}
}
