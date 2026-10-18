package main

func productCLIManifest() []cliSub {
	app := cliFlag{Name: "app", Value: "SLUG", Req: true, Short: "application slug"}
	revision := cliFlag{Name: "revision", Value: "HASH", Short: "immutable catalog revision (default current)"}
	file := cliFlag{Name: "file", Value: "PATH", Short: "catalog JSON file (default product.json)"}
	usage := []cliFlag{app, {Name: "tenant", Value: "UUID", Short: "customer platform tenant"}, {Name: "self", Bool: true, Short: "use authenticated customer's view"}, {Name: "period", Value: "YYYY-MM", Short: "UTC usage month (default current)"}}
	return []cliSub{
		{Name: "email", Short: "Inspect notification delivery and retry eligible provider rejections", Subcommands: []cliSub{
			{Name: "recoveries", Short: "Read scoped notification recovery receipts; never resumes sending", Flags: []cliFlag{app, {Name: "tenant", Value: "UUID", Short: "optional customer platform tenant"}, {Name: "cursor", Value: "CURSOR", Short: "next recovery history cursor"}}},
			{Name: "opt-outs", Short: "Read scoped notification opt-out receipts", Flags: []cliFlag{app, {Name: "tenant", Value: "UUID", Short: "optional customer platform tenant"}, {Name: "cursor", Value: "CURSOR", Short: "next opt-out history cursor"}}},
			{Name: "list", Short: "Read retained queue totals and one page of safe delivery diagnostics", Flags: []cliFlag{app, {Name: "tenant", Value: "UUID", Short: "optional customer platform tenant"}, {Name: "status", Value: "STATUS", Short: "all, queued, leased, captured, provider_accepted, delivered, bounced, complained, failed or cancelled"}, {Name: "cursor", Value: "CURSOR", Short: "next email history cursor"}}},
			{Name: "retry", Short: "Requeue one eligible provider rejection with its remaining budget and deadline", Flags: []cliFlag{app, {Name: "tenant", Value: "UUID", Req: true, Short: "expected customer platform tenant"}, {Name: "delivery", Value: "UUID", Req: true, Short: "delivery reference from email history"}, {Name: "attempt", Value: "N", Req: true, Short: "observed failed attempt; preserve these values for lost-response retries"}}},
		}},
		{Name: "team-policy", Short: "Review which workspace roles team managers may manage", Subcommands: []cliSub{
			{Name: "inspect", Short: "Read the current team role delegation", Flags: []cliFlag{app}},
			{Name: "review", Short: "Save a private delegation review without changing memberships", Flags: []cliFlag{app, {Name: "policy-file", Value: "PATH", Req: true, Short: "JSON manager-role to delegated-role policy"}, {Name: "file", Value: "PATH", Short: "new review file (default product-team-policy-review.json)"}}},
			{Name: "apply", Short: "Publish the saved delegation against its reviewed catalog revision", Flags: []cliFlag{app, {Name: "file", Value: "PATH", Short: "saved review; preserve for retries"}}},
		}},
		{Name: "plan-policy", Short: "Review the plan changes customers may choose", Subcommands: []cliSub{
			{Name: "inspect", Short: "Read the current offered plan transitions", Flags: []cliFlag{app}},
			{Name: "review", Short: "Save a private plan policy review without changing customers", Flags: []cliFlag{app, {Name: "policy-file", Value: "PATH", Req: true, Short: "JSON source-to-target plan transitions"}, {Name: "file", Value: "PATH", Short: "new review file (default product-plan-policy-review.json)"}}},
			{Name: "apply", Short: "Apply the saved policy against its original catalog revision", Flags: []cliFlag{app, {Name: "file", Value: "PATH", Short: "saved review; preserve for retries"}}},
		}},
		{Name: "access", Short: "Review and configure customer permissions for live Auth roles", Subcommands: []cliSub{
			{Name: "inspect", Short: "Read the current customer access policy", Flags: []cliFlag{app}},
			{Name: "review", Short: "Save a private review without changing access", Flags: []cliFlag{app, {Name: "policy-file", Value: "PATH", Req: true, Short: "JSON role-to-permission policy"}, {Name: "file", Value: "PATH", Short: "new private review file (default product-access-review.json)"}}},
			{Name: "apply", Short: "Apply a saved access review against its original revision", Flags: []cliFlag{app, {Name: "file", Value: "PATH", Short: "saved review; preserve for lost-response retries"}}},
		}},
		{Name: "doctor", Short: "Observe product configuration; native qualification remains separate", Flags: []cliFlag{app, {Name: "tenant", Value: "UUID", Short: "customer for entitlement, allowance and execution checks"}}},
		{Name: "pilot", Short: "Verify three-customer receipts, help and journey evidence", Subcommands: []cliSub{
			{Name: "init", Short: "Create an explicit pilot manifest without overwriting files", Flags: []cliFlag{app, {Name: "file", Value: "PATH", Short: "new manifest (default product-pilot.json)"}, {Name: "feature", Value: "NAME", Short: "feature (default export)"}}},
			{Name: "help", Short: "Inspect scoped help hashes without printing customer text", Flags: []cliFlag{app, {Name: "tenant", Value: "UUID", Req: true, Short: "expected customer platform tenant"}, {Name: "request", Value: "UUID", Req: true, Short: "retained Customer Help request"}}},
			{Name: "verify", Short: "Compare receipts, help and local journey records; never executes work", Flags: []cliFlag{app, {Name: "file", Value: "PATH", Short: "pilot manifest (default product-pilot.json)"}, {Name: "output", Value: "PATH", Short: "new private JSON report; no overwrite"}}},
		}},
		{Name: "init", Short: "Create a local paid-export catalog without overwriting files", Flags: []cliFlag{file, {Name: "auth-app", Value: "SLUG", Short: "Application Auth app for customer login"}, {Name: "definition", Value: "UUID", Short: "pin an existing operation definition"}, {Name: "operation", Value: "NAME", Short: "follow a named Operation across deployments"}, {Name: "scope", Value: "SCOPE", Short: "Operation environment (default default)"}}},
		{Name: "validate", Short: "Validate a local catalog and print its revision", Flags: []cliFlag{file}},
		{Name: "apply", Short: "Publish an immutable catalog revision (internal flag required)", Flags: []cliFlag{app, file}},
		{Name: "inspect", Short: "Read a catalog", Flags: []cliFlag{app, revision}},
		{Name: "features", Short: "Inspect feature bindings", Subcommands: []cliSub{{Name: "list", Short: "List features", Flags: []cliFlag{app, revision}}}},
		{Name: "plans", Short: "Inspect product plans", Subcommands: []cliSub{{Name: "list", Short: "List plans", Flags: []cliFlag{app, revision}}}},
		{Name: "customers", Short: "Assign and inspect customer product plans", Subcommands: []cliSub{
			{Name: "workspaces", Short: "List live Auth workspaces you own or administer", Flags: []cliFlag{app, {Name: "auth-token-file", Value: "PATH", Req: true, Short: "private short-lived Application Auth user access-token file"}}},
			{Name: "onboard", Short: "Review customer setup, then confirm the saved review safely", Flags: productOnboardingCLIFlags(app)},
			{Name: "onboarding", Short: "Read retained setup and observed customer activation progress", Flags: []cliFlag{app, {Name: "id", Value: "UUID", Req: true, Short: "onboarding receipt UUID"}}},
			{Name: "list", Short: "List one page of assigned customers", Flags: []cliFlag{app, {Name: "cursor", Value: "CURSOR", Short: "next page cursor"}}},
			{Name: "overview", Short: "Observe readiness, guided activation and unresolved work for assigned customers", Flags: []cliFlag{app, {Name: "cursor", Value: "UUID", Short: "next customer page cursor"}}},
			{Name: "activity", Short: "Read one page of retained customer setup, plan, work and billing history", Flags: []cliFlag{app, {Name: "tenant", Value: "UUID", Req: true, Short: "customer platform tenant"}, {Name: "cursor", Value: "CURSOR", Short: "next activity page cursor"}}},
			{Name: "assign", Short: "Assign a revisioned plan or pause new starts", Flags: []cliFlag{app, revision, {Name: "tenant", Value: "UUID", Req: true, Short: "customer platform tenant"}, {Name: "plan", Value: "NAME", Req: true, Short: "product plan"}, {Name: "auth-org", Value: "UUID", Short: "Application Auth organization (empty unlinks login)"}, {Name: "suspended", Bool: true, Short: "pause new feature starts"}}},
		}},
		{Name: "quote", Short: "Read current customer price and allowance before confirming a run", Flags: []cliFlag{app, {Name: "feature", Value: "NAME", Req: true, Short: "assigned feature"}}},
		{Name: "start", Short: "Start an entitled feature using a tenant credential", Flags: []cliFlag{app, {Name: "quote", Value: "HASH", Req: true, Short: "confirmed quote hash; preserve for retries"}, {Name: "feature", Value: "NAME", Req: true, Short: "assigned feature"}, {Name: "input", Value: "PATH", Req: true, Short: "input JSON file"}, {Name: "key", Value: "KEY", Req: true, Short: "stable idempotency key; preserve it across retries"}}},
		{Name: "explain", Short: "Trace an accepted run's entitlement, binding, execution and charge", Positionals: []string{"<operation-id>"}, Flags: []cliFlag{app, {Name: "after", Value: "N", Short: "execution generation cursor (default 0)"}}},
		{Name: "usage", Short: "Read outcome usage and start allowances", Flags: usage},
		{Name: "statements", Short: "Review and finalize charges, issue credit notes, and record invoice handoffs", Subcommands: productBillingCLIManifest(app)},
	}
}

func productOnboardingCLIFlags(app cliFlag) []cliFlag {
	return []cliFlag{app,
		{Name: "file", Value: "PATH", Short: "private review file (default product-customer.json); preserve for retries"},
		{Name: "auth-token-file", Value: "PATH", Short: "private short-lived Application Auth user access-token file"},
		{Name: "tenant", Value: "UUID", Short: "existing customer platform tenant"},
		{Name: "external-ref", Value: "REF", Short: "stable reference for a new customer"},
		{Name: "name", Value: "NAME", Short: "new customer name"},
		{Name: "workspace", Value: "SELECTOR", Short: "exact Auth workspace slug, name or UUID"},
		{Name: "plan", Value: "NAME", Short: "product plan to review"},
		{Name: "confirm", Bool: true, Short: "apply the saved review; use the same file for retries"},
	}
}

func productBillingCLIManifest(app cliFlag) []cliSub {
	tenant := cliFlag{Name: "tenant", Value: "UUID", Short: "customer platform tenant"}
	period := cliFlag{Name: "period", Value: "YYYY-MM", Short: "UTC admission month (default current)"}
	self := cliFlag{Name: "self", Bool: true, Short: "use authenticated customer's billing view"}
	cursor := cliFlag{Name: "cursor", Value: "UUID", Short: "next page cursor"}
	operation := cliFlag{Name: "operation", Value: "UUID", Req: true, Short: "logical operation"}
	reason := cliFlag{Name: "reason", Value: "TEXT", Req: true, Short: "permanent billing decision reason; no secrets"}
	document := cliFlag{Name: "document", Value: "UUID", Req: true, Short: "statement or credit note"}
	return []cliSub{
		{Name: "list", Short: "List finalized statements and credit notes for a month", Flags: []cliFlag{app, tenant, period, self, cursor}},
		{Name: "review", Short: "Review charges, unresolved runs, and the confirmation hash", Flags: []cliFlag{app, tenant, period, self}},
		{Name: "receipts", Short: "List one page of retained billing receipts", Flags: []cliFlag{app, tenant, period, cursor}},
		{Name: "finalize", Short: "Freeze a reviewed, resolved, closed UTC month", Flags: []cliFlag{app, tenant, period, {Name: "review-hash", Value: "HASH", Req: true, Short: "confirmed review hash; preserve for retries"}}},
		{Name: "waive", Short: "Permanently waive an unresolved run's charge; does not cancel execution", Flags: []cliFlag{app, tenant, period, operation, reason}},
		{Name: "credit", Short: "Issue a credit note against a finalized successful run", Flags: []cliFlag{app, tenant, period, operation, reason, {Name: "id", Value: "UUID", Req: true, Short: "stable credit note ID; preserve for retries"}, {Name: "amount-millicents", Value: "N", Req: true, Short: "positive credit amount"}}},
		{Name: "handoff", Short: "Record one invoice reference for a statement or credit note", Flags: []cliFlag{app, tenant, period, document, {Name: "invoice-id", Value: "REFERENCE", Req: true, Short: "stable external invoice or credit note reference"}}},
		{Name: "handoff-status", Short: "Recover the retained invoice handoff after a lost response", Flags: []cliFlag{app, tenant, period, document}},
	}
}
