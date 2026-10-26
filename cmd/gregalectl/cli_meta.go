// cli_meta.go — operator-side CLI manifest (issue #911 / ADR-110 PR-6.5).
//
// Hand-curated manifest of every top-level gregalectl command, used as
// the single source of truth for `gregalectl completion {bash|zsh|fish|powershell}`
// and `gregalectl man [command]`. Mirrors the dispatch switch in
// main.go (cmd/gregalectl/main.go::run); the manifest-drift test
// (commands_completion_test.go::TestCompletion_ManifestDrift) walks
// main.go and asserts every `case "<name>":` arm has a matching
// cliCommand entry, and vice versa.
//
// This is the operator half of the original cmd/gregale/cli_meta.go.
// PR-6.5 atomic split: customer commands stay in cmd/gregale/cli_meta.go,
// operator commands live here. New operator commands add a 4-line entry
// here at the same time as the `case "<name>":` in main.go — the
// code-review gate fires when either side is missing.
//
// PR-7 (cutover runbook) may extract a shared `cmd/internal/cliutil/`
// package; for PR-6.5 the type definitions below are duplicated into
// cmd/gregale/cli_meta.go (identical shapes) and the operator binary
// is self-contained.

package main

// cliCommand is one top-level gregalectl command. Mirrors
// cmd/gregale/cli_meta.go::cliCommand byte-for-byte (PR-6.5).
type cliCommand struct {
	Name        string
	DocSlug     string
	Short       string
	Subcommands []cliSub
	Flags       []cliFlag
	Positionals []string
	ClosedSet   []string
}

func (c cliCommand) hasSlugFirst() bool {
	return len(c.Positionals) > 0 && c.Positionals[0] == "<slug>"
}

// cliSub is one verb under a cliCommand. Mirrors cmd/gregale/cli_meta.go.
type cliSub struct {
	Name  string
	Short string
	Flags []cliFlag
}

// cliFlag is one CLI flag. Mirrors cmd/gregale/cli_meta.go.
type cliFlag struct {
	Name      string
	Short     string
	Req       bool
	ClosedSet []string
}

// cliCommands is the operator-side manifest. One entry per top-level
// command in main.go's run() switch.
//
// Operator-side surface (PR-6.5):
//   - manifest        (validate | render | ansible)
//   - release         (bundle | install | kgv verify | kgv rotate | kgv init [alias] | reconcile)
//   - doctor          (read-only cluster diagnostic)
//   - host-age        (init | rotate | status | prune-previous)
//   - pki             (init | status | rotate | list)
//   - sign-keys       (init | rotate | status)
//   - node-key        (init | rotate | status)
//   - workload-identity (init | rotate | status)
//   - backup          (init | unseal-rclone | unseal-archive-creds)
//   - secrets         (init | rotate | status | stamp)
//   - artifact        (publish | verify)
//   - compute-nodes   (add | list | show | drain | drain-status | activate | force-drain | retire)
//   - release-acceptance (mint-token | revoke-token)
//   - deploy          (join-node | add-node)
//   - obs             (health | incidents | overview | capacity)
//   - debug           (otel-smoke; ADR-127 PR-D)
//   - github          (status | retry-delivery | retry-check)
//   - jobs            (active | inspect | cancel)
//   - deployments     (active | inspect | cancel | retry)
//   - trusted-publishers (add | remove | list) — see ADR-058 deviation note in main.go:15
//   - version         (internal)
//   - completion      (bash | zsh | fish | powershell) (internal)
//   - man             (<command>) (internal)
//
// Customer-side commands (rollback, secrets, registry, deploy, init,
// apps, deploy, etc.) stay in cmd/gregale/cli_meta.go. The drift
// test in each binary's commands_completion_test.go pins the
// boundary — adding a customer command to gregalectl or an operator
// command to gregale fails CI immediately.
var cliCommands = []cliCommand{
	{Name: "promotion-rollout", DocSlug: "promotion-rollout", Short: "Inspect readiness and manage signed promotion rollout approvals", Subcommands: []cliSub{
		{Name: "readiness", Short: "Check a staged policy against an exact APID revision and target", Flags: []cliFlag{{Name: "policy", Short: "rollout policy file"}, {Name: "trust-key", Short: "trusted public key"}, {Name: "revision", Short: "full APID source revision", Req: true}, {Name: "account-id", Short: "account UUID", Req: true}, {Name: "project-id", Short: "project UUID", Req: true}, {Name: "enabled-capabilities", Short: "configured APID boot flags"}, {Name: "require", Short: "capabilities needed for success (default health)"}}},
		{Name: "keygen", Short: "Create a private signer and public trust key", Flags: []cliFlag{{Name: "sign-key", Short: "new private key path", Req: true}, {Name: "trust-key", Short: "new public key path", Req: true}}},
		{Name: "sign", Short: "Sign reviewed, complete qualification and runbook evidence", Flags: []cliFlag{{Name: "input", Short: "approval input JSON", Req: true}, {Name: "sign-key", Short: "private signing key", Req: true}, {Name: "native-log", Short: "reviewed combined native log", Req: true}, {Name: "service-graph-log", Short: "reviewed supplemental log required for service_graph"}, {Name: "output", Short: "new signed approval file", Req: true}}},
		{Name: "pause", Short: "Stop new admissions without changing accepted operations", Flags: []cliFlag{{Name: "policy", Short: "rollout policy file", Req: true}}},
		{Name: "resume", Short: "Reopen only an already approved rollout scope", Flags: []cliFlag{{Name: "policy", Short: "rollout policy file", Req: true}}},
	}},
	{
		Name: "ci", DocSlug: "ci", Short: "Internal, default-off CI planning and execution controls",
		Subcommands: []cliSub{
			{Name: "validate", Short: "Validate a local pipeline without running steps", Flags: []cliFlag{{Name: "file", Short: "local CI definition"}}},
			{Name: "plan", Short: "Compile a local immutable pipeline plan", Flags: []cliFlag{{Name: "file", Short: "local CI definition"}}},
			{Name: "capabilities", Short: "Inspect internal CI availability"},
			{Name: "health", Short: "Inspect internal CI queues, workers and quota headroom"},
			{Name: "checks", Short: "List, inspect or retry exhausted execution Check delivery", Flags: []cliFlag{
				{Name: "id", Short: "execution UUID"}, {Name: "key", Short: "stable retry request identity"}, {Name: "generation", Short: "current retry_generation from Check status"},
			}},
			{Name: "put", Short: "Save an immutable pipeline revision", Flags: []cliFlag{{Name: "name", Short: "pipeline name", Req: true}, {Name: "file", Short: "local CI definition"}}},
			{Name: "revisions", Short: "List saved pipeline revisions"},
			{Name: "revision", Short: "Read a saved revision", Flags: []cliFlag{{Name: "id", Short: "revision UUID", Req: true}}},
			{Name: "run", Short: "Create a blocked metadata receipt", Flags: []cliFlag{{Name: "revision", Short: "revision UUID", Req: true}, {Name: "commit", Short: "full commit SHA", Req: true}, {Name: "key", Short: "stable receipt identity", Req: true}}},
			{Name: "runs", Short: "List blocked metadata receipts"},
			{Name: "status", Short: "Read a metadata receipt", Flags: []cliFlag{{Name: "id", Short: "receipt UUID", Req: true}}},
			{Name: "events", Short: "Read metadata receipt events", Flags: []cliFlag{{Name: "id", Short: "receipt UUID", Req: true}}},
			{Name: "cancel", Short: "Cancel a metadata receipt", Flags: []cliFlag{{Name: "id", Short: "receipt UUID", Req: true}}},
			{Name: "github", Short: "Bind, list or disable account-owned GitHub sources", Flags: []cliFlag{{Name: "name", Short: "binding name"}, {Name: "revision", Short: "revision UUID"}, {Name: "installation", Short: "GitHub installation ID"}, {Name: "repository-id", Short: "immutable repository ID"}, {Name: "push-branches", Short: "exact push branches"}, {Name: "pr-branches", Short: "exact PR base branches"}}},
			{Name: "execution", Short: "Submit, inspect, cancel, read output or archive completed execution history", Flags: []cliFlag{
				{Name: "profile", Short: "saved execution profile UUID; replaces revision/binding/template"},
				{Name: "id", Short: "execution UUID"}, {Name: "revision", Short: "revision UUID"}, {Name: "commit", Short: "full commit SHA"}, {Name: "binding", Short: "verified binding UUID"}, {Name: "key", Short: "stable execution identity"}, {Name: "template", Short: "JOB=materialized Jobs UUID; repeat for each job"},
				{Name: "attempt", Short: "attempt UUID for logs"}, {Name: "offset", Short: "log byte offset"}, {Name: "limit", Short: "log page byte bound"}, {Name: "job", Short: "artifact job name"}, {Name: "path", Short: "verified artifact path"}, {Name: "output", Short: "new private artifact file"},
			}},
			{Name: "profile", Short: "Save, list, inspect or disable immutable execution profiles", Flags: []cliFlag{
				{Name: "name", Short: "immutable profile name"}, {Name: "id", Short: "profile UUID"}, {Name: "revision", Short: "revision UUID"}, {Name: "binding", Short: "verified binding UUID"}, {Name: "template", Short: "JOB=materialized Jobs UUID; repeat for each job"},
			}},
			{Name: "automation", Short: "Configure explicit branch-push admission through a saved profile", Flags: []cliFlag{
				{Name: "binding", Short: "verified binding UUID"}, {Name: "profile", Short: "saved profile UUID"}, {Name: "push-branches", Short: "exact trusted push branches"},
			}},
		},
	},
	{
		Name:    dispatchBilling,
		DocSlug: "billing",
		Short:   "Operator billing catalog, reconciliation, and webhook diagnostics",
		Subcommands: []cliSub{
			{Name: "price-catalog", Short: "List, sync, or reset the provider catalog"},
			{Name: "reconcile", Short: "Reconcile usage for one account"},
			{Name: "reconcile-paddle-overage", Short: "Check Paddle overage schema readiness"},
			{Name: "webhook-test", Short: "Send a locally signed Paddle or Stripe webhook"},
		},
	},
	{
		Name:    "status",
		DocSlug: "status",
		Short:   "Publish and inspect public incidents and maintenance",
		Subcommands: []cliSub{
			{Name: "incident", Short: "Create, update, resolve, or list incidents"},
			{Name: "maintenance", Short: "Schedule, update, start, complete, cancel, or list maintenance"},
		},
	},
	{
		Name:    dispatchReleaseAcceptance,
		DocSlug: "release-acceptance",
		Short:   "Run production release acceptance credential and placement checks",
		Subcommands: []cliSub{
			{Name: "mint-token", Short: "Mint a short-lived token for the production ingress gate", Flags: []cliFlag{{Name: "ttl", Short: "credential lifetime (15m..6h)"}, {Name: "yes", Short: "acknowledge the production database mutation", Req: true}, {Name: "reason", Short: "fixed production_release_acceptance audit reason", Req: true}}},
			{Name: "revoke-token", Short: "Revoke one production ingress gate token", Flags: []cliFlag{{Name: "key-id", Short: "acceptance API key id", Req: true}, {Name: "yes", Short: "acknowledge the production database mutation", Req: true}, {Name: "reason", Short: "fixed production_release_acceptance audit reason", Req: true}}},
			{Name: "verify-placement", Short: "Require every active node plus both app and function shapes", Flags: []cliFlag{{Name: "slugs", Short: "comma-separated acceptance app slugs", Req: true}}},
		},
	},
	{
		Name:    dispatchOperatorAuth,
		DocSlug: "operator-auth",
		Short:   "Manage the MFA-stepped-up session used for provider mutations",
		Subcommands: []cliSub{
			{Name: "login", Short: "Sign in and verify TOTP", Flags: []cliFlag{{Name: "email", Short: "FAAS_ADMIN_EMAILS-allowlisted operator email", Req: true}}},
			{Name: "step-up", Short: "Refresh the five-minute TOTP step-up window", Flags: []cliFlag{{Name: "force", Short: "verify a new code even when the last step-up is under a minute old"}}},
			{Name: "status", Short: "Validate and describe the current operator session"},
			{Name: "logout", Short: "Revoke and remove the current operator session"},
		},
	},
	{
		Name:    dispatchGithub,
		DocSlug: "github-recovery",
		Short:   "Inspect and retry durable GitHub delivery and Check Run work",
		Subcommands: []cliSub{
			{
				Name:  "status",
				Short: "List webhook deliveries and Check Run updates by status",
				Flags: []cliFlag{
					{Name: "status", Short: "pending|processing|succeeded|dead (default dead)"},
					{Name: "limit", Short: "maximum rows per queue (1..500)"},
					{Name: "json", Short: "emit structured JSON"},
				},
			},
			{
				Name:  "retry-delivery",
				Short: "Move one dead webhook delivery back to pending",
				Flags: []cliFlag{
					{Name: "delivery-id", Short: "X-GitHub-Delivery id", Req: true},
					{Name: "reason", Short: "durable audit reason slug", Req: true},
					{Name: "trace-id", Short: "32-char hex trace id (generated when omitted)"},
					{Name: "yes", Short: "acknowledge retrying customer deployment work", Req: true},
				},
			},
			{
				Name:  "retry-check",
				Short: "Move one dead Check Run update back to pending",
				Flags: []cliFlag{
					{Name: "deployment-id", Short: "deployment id", Req: true},
					{Name: "reason", Short: "durable audit reason slug", Req: true},
					{Name: "trace-id", Short: "32-char hex trace id (generated when omitted)"},
					{Name: "yes", Short: "acknowledge retrying the GitHub Check Run write", Req: true},
				},
			},
		},
	},
	{
		Name:    dispatchArtifact,
		DocSlug: "artifact",
		Short:   "Publish or verify release-pinned shared artifacts",
		Subcommands: []cliSub{
			{
				Name:  "publish",
				Short: "Publish the release-pinned kernel to the configured storage backend",
				Flags: []cliFlag{
					{Name: "env-file", Short: "storage.env path (required)"},
					{Name: "manifest-file", Short: "production manifest path (required)"},
					{Name: "file", Short: "release vmlinux path (required for publish)"},
					{Name: "no-cache", Short: "bypass the local read-through cache"},
				},
			},
			{
				Name:  "verify",
				Short: "Verify the release-pinned kernel exists with the anchored digest",
				Flags: []cliFlag{
					{Name: "env-file", Short: "storage.env path (required)"},
					{Name: "manifest-file", Short: "production manifest path (required)"},
					{Name: "no-cache", Short: "bypass the local read-through cache"},
					{Name: "refresh", Short: "fetch from shared storage and replace the local cache"},
				},
			},
		},
	},
	{
		Name:    "backup",
		DocSlug: "backup",
		Short:   "Operator rclone config unseal (backup unseal-rclone | unseal-archive-creds)",
		Subcommands: []cliSub{
			{Name: "unseal-rclone", Short: "Unseal the rclone config"},
			{Name: "unseal-archive-creds", Short: "Unseal the log-archive credentials"},
			{Name: "init", Short: "Initialise the on-disk backup credentials store"},
			{Name: "seal-cp-state", Short: "Encrypt the control plane's host-local state for off-host backup"},
			{Name: "restore-cp-state", Short: "Decrypt a control-plane state backup into the takeover stage"},
		},
	},
	{
		Name:    dispatchHostAge,
		DocSlug: "host-age",
		Short:   "Operator host.age rotation (host-age init|rotate|status|prune-previous)",
		Subcommands: []cliSub{
			{Name: subHostAgeInit, Short: "Initialise host.age"},
			{Name: subHostAgeRotate, Short: "Rotate host.age"},
			{Name: subHostAgeStatus, Short: "Show host.age status"},
			{Name: subHostAgePrunePrevious, Short: "Prune the previous host.age key"},
		},
	},
	{
		Name:    dispatchFleetSeal,
		DocSlug: "fleet-seal",
		Short:   "Provision, migrate, and verify the fleet-wide sealed-secret identity",
		Subcommands: []cliSub{
			{Name: "init", Short: "Create fleet.age and fleet.age.pub", Flags: []cliFlag{{Name: "dir", Short: "fleet identity directory"}, {Name: "force", Short: "coordinated identity replacement"}, {Name: "json", Short: "emit structured JSON"}}},
			{Name: "retire-release-key", Short: "Verify and retire a recipient from release-secret storage", Flags: []cliFlag{{Name: "kid", Short: "previous age recipient"}, {Name: "current-key", Short: "current fleet identity"}, {Name: "pg-dsn", Short: "PostgreSQL DSN"}, {Name: "db-env", Short: "database environment file"}, {Name: "dry-run", Short: "read dependencies without retiring"}, {Name: "json", Short: "emit retirement status"}}},
			{Name: "migrate", Short: "Re-seal cluster and customer material to fleet.age", Flags: []cliFlag{{Name: "fleet-key", Short: "fleet.age identity"}, {Name: "legacy-host-dir", Short: "legacy host identity directory"}, {Name: "control-key", Short: "control seal-domain identity"}, {Name: "pg-dsn", Short: "PostgreSQL DSN"}, {Name: "db-env", Short: "file containing DATABASE_URL"}, {Name: "json", Short: "emit structured JSON"}}},
			{Name: "verify", Short: "Prove customer probe and shared-kid JWT access", Flags: []cliFlag{{Name: "fleet-key", Short: "staged fleet.age identity"}, {Name: "host-key", Short: "per-host host.age identity"}, {Name: "control-key", Short: "control seal-domain identity"}, {Name: "pg-dsn", Short: "PostgreSQL DSN"}, {Name: "db-env", Short: "file containing DATABASE_URL"}, {Name: "metrics-file", Short: "node_exporter textfile output"}, {Name: "json", Short: "emit structured JSON"}}},
		},
	},
	{
		Name:    dispatchBreakGlass,
		DocSlug: "break-glass",
		Short:   "Incident containment: pause releases platform-wide, revoke every control-plane credential (ADR 20261022140000)",
		Subcommands: []cliSub{
			{Name: "pause", Short: "Refuse new deployments and hold queued builds platform-wide", Flags: []cliFlag{{Name: "reason", Short: "why (recorded)"}, {Name: "pg-dsn", Short: "PostgreSQL DSN"}, {Name: "db-env", Short: "file containing DATABASE_URL"}, {Name: "json", Short: "emit structured JSON"}}},
			{Name: "resume", Short: "Lift the release pause", Flags: []cliFlag{{Name: "reason", Short: "why (recorded)"}, {Name: "pg-dsn", Short: "PostgreSQL DSN"}, {Name: "db-env", Short: "file containing DATABASE_URL"}, {Name: "json", Short: "emit structured JSON"}}},
			{Name: "status", Short: "Show the release pause and live credential counts", Flags: []cliFlag{{Name: "pg-dsn", Short: "PostgreSQL DSN"}, {Name: "db-env", Short: "file containing DATABASE_URL"}, {Name: "json", Short: "emit structured JSON"}}},
			{Name: "revoke-credentials", Short: "Revoke every live session, API key and access token (dry run without --yes)", Flags: []cliFlag{{Name: "kinds", Short: "credential kinds to revoke"}, {Name: "reason", Short: "why (recorded)"}, {Name: "yes", Short: "revoke instead of counting"}, {Name: "pg-dsn", Short: "PostgreSQL DSN"}, {Name: "db-env", Short: "file containing DATABASE_URL"}, {Name: "json", Short: "emit structured JSON"}}},
		},
	},
	{
		Name:    dispatchHoneytoken,
		DocSlug: "honeytoken",
		Short:   "Plant and manage honeytokens: decoy API keys that page when presented (ADR 20261024014500)",
		Subcommands: []cliSub{
			{Name: "plant", Short: "Mint a decoy API key, register it and seal it into a never-deployed app (or print it once)", Flags: []cliFlag{{Name: "label", Short: "opaque reference, not the location"}, {Name: "app-id", Short: "never-deployed app to plant in"}, {Name: "var", Short: "secret name"}, {Name: "recipient-file", Short: "recipient app secrets are sealed to"}, {Name: "hmac-key", Short: "apid's host HMAC key"}, {Name: "print", Short: "print the decoy once instead"}, {Name: "pg-dsn", Short: "PostgreSQL DSN"}, {Name: "db-env", Short: "file containing DATABASE_URL"}, {Name: "json", Short: "emit structured JSON"}}},
			{Name: "list", Short: "List registered honeytokens", Flags: []cliFlag{{Name: "pg-dsn", Short: "PostgreSQL DSN"}, {Name: "db-env", Short: "file containing DATABASE_URL"}, {Name: "json", Short: "emit structured JSON"}}},
			{Name: "retire", Short: "Mark a honeytoken retired; it still pages if presented", Flags: []cliFlag{{Name: "id", Short: "honeytoken id"}, {Name: "pg-dsn", Short: "PostgreSQL DSN"}, {Name: "db-env", Short: "file containing DATABASE_URL"}, {Name: "json", Short: "emit structured JSON"}}},
		},
	},
	{
		Name:    dispatchObjectStorage,
		DocSlug: "object-storage",
		Short:   "Reconcile the object bucket catalog with each provider after a database restore (ADR 20261025111015)",
		Subcommands: []cliSub{
			{Name: "reconcile", Short: "List each backend's Gregale buckets and report catalog disagreements, read-only (exit 4 on findings)", Flags: []cliFlag{{Name: "config", Short: "provider registry JSON"}, {Name: "provider-env", Short: "provider credential env file"}, {Name: "pg-dsn", Short: "PostgreSQL DSN"}, {Name: "db-env", Short: "file containing DATABASE_URL"}, {Name: "json", Short: "emit structured JSON"}}},
		},
	},
	{
		Name:    dispatchSealDomain,
		DocSlug: "seal-domain",
		Short:   "Provision, verify, and migrate per-reader seal domains (ADR 20261022111812)",
		Subcommands: []cliSub{
			{Name: "init", Short: "Create <domain>.age and <domain>.age.pub", Flags: []cliFlag{{Name: "domain", Short: "seal domain"}, {Name: "dir", Short: "domain identity directory"}, {Name: "force", Short: "coordinated identity replacement"}, {Name: "json", Short: "emit structured JSON"}}},
			{Name: "verify", Short: "Prove this node's domain identity opens the domain probe", Flags: []cliFlag{{Name: "domain", Short: "seal domain"}, {Name: "dir", Short: "domain identity directory"}, {Name: "publish", Short: "write the probe when the domain has none"}, {Name: "pg-dsn", Short: "PostgreSQL DSN"}, {Name: "db-env", Short: "file containing DATABASE_URL"}, {Name: "metrics-file", Short: "node_exporter textfile output"}, {Name: "recipient-only", Short: "check the staged recipient against the published probe"}, {Name: "json", Short: "emit structured JSON"}}},
			{Name: "migrate", Short: "Re-seal a domain's stored envelopes", Flags: []cliFlag{{Name: "domain", Short: "seal domain (outbound, runtime, source, build, edge, alerting, storage or control)"}, {Name: "dir", Short: "domain identity directory"}, {Name: "fleet-key", Short: "fleet.age identity"}, {Name: "host-key", Short: "legacy host.age identity"}, {Name: "readers-verified", Short: "every reader holds the domain identity"}, {Name: "pg-dsn", Short: "PostgreSQL DSN"}, {Name: "db-env", Short: "file containing DATABASE_URL"}, {Name: "json", Short: "emit structured JSON"}}},
		},
	},
	{
		// PR-911 image rollout (PR #929 mega; ADR-110 + ADR-111). Operator
		// surfaces draining + activation of compute_nodes rows so the
		// deployctl upgrade-node orchestrator can reason about which box
		// is eligible for placement. The `add` verb is the operator-side
		// pre-registration path — it POSTs a row before vmmd boots on the
		// new box (multi-host scale-out gap #1; PR-A in the
		// tier-1-scaleout pair).
		Name:    dispatchComputeNodes,
		DocSlug: "compute-nodes",
		Short:   "Compute-node state machine (compute-nodes add|list|show|release-status|drain|drain-status|activate|force-drain|retire)",
		Subcommands: []cliSub{
			{
				Name:  "add",
				Short: "Pre-register a compute node (POST /v1/compute-nodes; the operator's target_url wins on conflict)",
				Flags: []cliFlag{
					{Name: "name", Short: "fqdn / short-hostname of the new node (required)"},
					{Name: "target-url", Short: "routable dial target for vmmd (tcp://vmmd-N.faas:50051 or unix://...)"},
					{Name: "gateway-target-url", Short: "private HTTP target for gatewayd-internal (tcp://host:port)"},
					{Name: "vpcpus", Short: "vCPU count reported to schedd"},
					{Name: "mem-mb", Short: "RAM MB reported to schedd"},
					{Name: "max-concurrency", Short: "max concurrent live instances"},
					{Name: "admission-ceiling-mb", Short: "tenant RAM admission ceiling (85% of mem-mb for production nodes)"},
					{Name: "from-file", Short: "JSON payload matching computeNodePayload (PR-B bridge)"},
					{Name: "defer-activation", Short: "insert/update the row drained until deployment readiness completes"},
					{Name: "reason", Short: "audit reason ([a-z0-9_]{1,64}; defaults to operator_node_enroll)"},
					{Name: "trace-id", Short: "32-character lowercase hex trace id (generated when empty)"},
					{Name: "break-glass-db", Short: "write directly to PostgreSQL during an apid outage"},
					{Name: "yes", Short: "acknowledge direct mutation with --break-glass-db"},
					{Name: "json", Short: "emit structured JSON to stdout"},
				},
			},
			{
				Name:  "list",
				Short: "List every registered compute node (default; --active-only filters; --json emits wire-shape)",
				Flags: []cliFlag{
					{Name: "active-only", Short: "filter to active=true rows only"},
					{Name: "json", Short: "emit structured JSON to stdout"},
					{Name: "break-glass-db", Short: "read directly during an apid outage"},
				},
			},
			{
				Name:  "show",
				Short: "Show one compute node's full row + live_instance_count (--node <fqdn>; --json emits wire-shape)",
				Flags: []cliFlag{
					{Name: "node", Short: "fqdn / short-hostname of the node to show (required)"},
					{Name: "json", Short: "emit structured JSON to stdout"},
					{Name: "break-glass-db", Short: "read directly during an apid outage"},
				},
			},
			{
				Name:  "release-status",
				Short: "Wait for every active node to observe a desired release with a fresh heartbeat",
				Flags: []cliFlag{
					{Name: "desired-release", Short: "40-character lowercase release git SHA", Req: true},
					{Name: "timeout", Short: "maximum convergence wait; zero performs one observation"},
					{Name: "poll-interval", Short: "interval between fleet observations"},
					{Name: "heartbeat-staleness", Short: "maximum age of a ready node heartbeat"},
					{Name: "json", Short: "emit desired and observed release state per node"},
					{Name: "break-glass-db", Short: "read directly during an apid outage"},
				},
			},
			{
				Name:  "drain",
				Short: "Submit and poll an authenticated node-drain intent",
				Flags: []cliFlag{
					{Name: "node", Short: "node fqdn", Req: true},
					{Name: "reason", Short: "durable audit reason"},
					{Name: "timeout", Short: "intent polling timeout"},
					{Name: "break-glass-db", Short: "bypass apid for reviewed database repair"},
					{Name: "yes", Short: "acknowledge break-glass database mutation"},
				},
			},
			{Name: "drain-status", Short: "Exit 0 only when the node is held in maintenance or retired", Flags: []cliFlag{{Name: "node", Short: "node fqdn", Req: true}, {Name: "break-glass-db", Short: "read directly during apid outage"}}},
			{
				Name:  "activate",
				Short: "Submit and poll an authenticated node-activation intent",
				Flags: []cliFlag{
					{Name: "node", Short: "node fqdn", Req: true},
					{Name: "reason", Short: "durable audit reason"},
					{Name: "timeout", Short: "intent polling timeout"},
					{Name: "break-glass-db", Short: "bypass apid for reviewed database repair"},
					{Name: "yes", Short: "acknowledge break-glass database mutation"},
				},
			},
			{
				Name:  "force-drain",
				Short: "Force a stuck node through the drain controller",
				Flags: []cliFlag{
					{Name: "node", Short: "node fqdn", Req: true},
					{Name: "reason", Short: "durable audit reason"},
					{Name: "timeout", Short: "intent polling timeout"},
					{Name: "yes", Short: "acknowledge possible cold eviction", Req: true},
					{Name: "break-glass-db", Short: "bypass apid for reviewed database repair"},
				},
			},
			{
				Name:  "retire",
				Short: "Permanently retire a maintenance-held node",
				Flags: []cliFlag{
					{Name: "node", Short: "node fqdn", Req: true},
					{Name: "reason", Short: "required durable audit reason", Req: true},
					{Name: "timeout", Short: "intent polling timeout"},
					{Name: "yes", Short: "confirm permanent retirement", Req: true},
					{Name: "break-glass-db", Short: "bypass apid for reviewed database repair"},
				},
			},
		},
	},
	{
		// Provider-neutral node adoption is the production path. The
		// legacy add-node surface remains documented for migration only.
		// Multi-host scale-out gap #2 (companion to gap #1 closed by
		// compute-nodes add). operator-side coordinator that writes
		// host_vars/<fqdn>.yml + hosts.ini + commits + ssh bootstrap
		// + POSTs the compute_nodes row. Closes the 5-step hand-curated
		// runbook into a single command.
		Name:    dispatchDeploy,
		DocSlug: "deploy",
		Short:   "Provider-neutral node adoption and fleet topology coordinator",
		Subcommands: []cliSub{
			{
				Name:  "claim",
				Short: "Validate a declarative provider handoff for one compute node",
				Flags: []cliFlag{
					{Name: "file", Short: "ComputeNodeClaim YAML/JSON file (required)"},
					{Name: "manifest-file", Short: "optional signed production manifest to check for the node"},
					{Name: "json", Short: "emit structured JSON"},
				},
			},
			{
				Name:  "fleet-bundle",
				Short: "Create or verify a signed, time-bounded fleet enrollment bundle",
				Flags: []cliFlag{
					{Name: "claim-file", Short: "provider-produced ComputeNodeClaim for create"},
					{Name: "file", Short: "signed FleetEnrollmentBundle YAML/JSON for validate"},
					{Name: "signature", Short: "detached cosign signature bundle for validate"},
					{Name: "manifest-file", Short: "production manifest membership check"},
					{Name: "name", Short: "fleet authorization name for create"},
					{Name: "generation", Short: "monotonically increasing generation for create"},
					{Name: "ttl", Short: "authorization lifetime for create"},
					{Name: "output", Short: "new output path for create; '-' writes stdout"},
					{Name: "cosign-binary", Short: "cosign verifier for validate"},
					{Name: "json", Short: "emit structured JSON for validate"},
				},
			},
			{
				Name:  "prepare-node",
				Short: "Prepare and cache verified release assets for provider-neutral node adoption",
				Flags: []cliFlag{
					{Name: "claim-file", Short: "provider-produced ComputeNodeClaim (alternative to nodes-file)"},
					{Name: "nodes-file", Short: "provider connection list (alternative to claim-file)"},
					{Name: "manifest-file", Short: "signed production manifest (required)"},
					{Name: "release-tag", Short: "signed release tag (required)"},
					{Name: "secrets-dir", Short: "directory containing fleet.age, fleet.age.pub, join secrets, and pki/ (required)"},
					{Name: "output-dir", Short: "prepared artifact directory (required)"},
					{Name: "cache-dir", Short: "persistent public artifact cache"},
					{Name: "cosign-binary", Short: "Linux/amd64 cosign binary to stage"},
					{Name: "release-repo", Short: "GitHub owner/repository"},
					{Name: "json", Short: "emit structured JSON"},
				},
			},
			{
				Name:  "join-fleet",
				Short: "Adopt a bounded batch of already-created compute hosts",
				Flags: []cliFlag{
					{Name: "nodes-file", Short: "YAML/JSON node list (required unless --claim-file is used)"},
					{Name: "claim-file", Short: "single ComputeNodeClaim YAML/JSON file (alternative to --nodes-file)"},
					{Name: "manifest-file", Short: "split-box manifest (required)"},
					{Name: "artifact-dir", Short: "standard shared join assets"},
					{Name: "runtime-bases-env", Short: "release-bound digest-pinned runtime base refs"},
					{Name: "fleet-age-key", Short: "shared fleet.age identity"},
					{Name: "fleet-age-recipient", Short: "matching fleet.age.pub recipient"},
					{Name: "max-parallel", Short: "bounded concurrent joins (default 4)"},
					{Name: "skip-fleet-preflight", Short: "skip one shared fleet preflight"},
					{Name: "resume", Short: "resume failed/interrupted joins"},
					{Name: "continue-on-error", Short: "continue reporting other node failures"},
					{Name: "dry-run", Short: "validate without contacting hosts"},
					{Name: "yes", Short: "approve the remote adoption"},
					{Name: "json", Short: "emit structured JSON"},
				},
			},
			{
				Name:  "converge-control-plane",
				Short: "Converge the control-plane bootstrap roles when their inputs changed (ADR-580)",
				Flags: []cliFlag{
					{Name: "manifest-file", Short: "signed split-box manifest (required)"},
					{Name: "ssh-known-hosts-file", Short: "verified known_hosts covering the control-plane host (required)"},
					{Name: "ansible-vars-file", Short: "control-plane Ansible variables (required)"},
					{Name: "ssh-user", Short: "SSH user (default root)"},
					{Name: "ssh-key", Short: "SSH private key"},
					{Name: "repo-root", Short: "repository root containing deploy/ansible"},
					{Name: "roles", Short: "converge only these comma-separated control-plane roles and record nothing (host-configuration lane)"},
					{Name: "json", Short: "emit structured JSON"},
				},
			},
			{
				Name:  "rollback-node",
				Short: "Drain a join row and record a resumable rollback",
				Flags: []cliFlag{
					{Name: "node", Short: "manifest node (required)"},
					{Name: "lease-ttl", Short: "rollback coordination lease"},
					{Name: "yes", Short: "confirm the drain"},
					{Name: "json", Short: "emit structured JSON"},
				},
			},
			{
				Name:  "join-node",
				Short: "Adopt an already-created machine: preflight, bootstrap, release, readiness, and control-plane activation",
				Flags: []cliFlag{
					{Name: "manifest-file", Short: "split-box manifest (required)"},
					{Name: "node", Short: "manifest compute-only host name (required)"},
					{Name: "ssh-host", Short: "SSH address of the already-created machine (required)"},
					{Name: "ssh-host-key-sha256", Short: "expected OpenSSH SHA256 host-key fingerprint"},
					{Name: "ssh-known-hosts-file", Short: "verified complete-fleet known_hosts file"},
					{Name: "ssh-user", Short: "SSH user (default root)"},
					{Name: "ssh-port", Short: "SSH port (default 22)"},
					{Name: "ssh-key", Short: "optional SSH private key"},
					{Name: "fleet-bundle-file", Short: "signed FleetEnrollmentBundle authorization"},
					{Name: "fleet-bundle-signature", Short: "detached cosign signature for the enrollment bundle"},
					{Name: "fleet-replay-state", Short: "durable single-use enrollment state directory"},
					{Name: "release-tarball", Short: "signed release.tar.gz"},
					{Name: "release-git-sha", Short: "optional signed release SHA override"},
					{Name: "bootstrap-binary", Short: "Linux bootstrap gregalectl"},
					{Name: "cosign-binary", Short: "cosign verifier"},
					{Name: "pki-dir", Short: "compute trust-bundle directory (CA private key is never copied)"},
					{Name: "sign-key", Short: "image-signing private key"},
					{Name: "verify-key", Short: "image-signing public key"},
					{Name: "compute-db-env", Short: "root-only compute DB environment"},
					{Name: "storage-env", Short: "shared remote storage environment"},
					{Name: "imaged-storage-env", Short: "imaged-only migration lifecycle credential"},
					{Name: "runtime-bases-env", Short: "release-bound digest-pinned runtime base refs"},
					{Name: "storage-device", Short: "optional dedicated fast-root block device"},
					{Name: "fleet-age-key", Short: "shared fleet.age identity"},
					{Name: "fleet-age-recipient", Short: "matching fleet.age.pub recipient"},
					{Name: "format-storage", Short: "explicitly format a supplied blank device as XFS"},
					{Name: "box-age-key", Short: "optional box-age identity source"},
					{Name: "rclone-envelope", Short: "encrypted rclone.conf envelope"},
					{Name: "archive-creds-envelope", Short: "encrypted archive credentials envelope"},
					{Name: "artifact-dir", Short: "standard directory containing join assets"},
					{Name: "ansible-vars-file", Short: "optional provider/overlay Ansible vars"},
					{Name: "skip-fleet-preflight", Short: "skip complete-fleet preflight"},
					{Name: "prepare-only", Short: "stage and verify without draining the node"},
					{Name: "activate-prepared", Short: "activate a previously prepared release"},
					{Name: "resume", Short: "resume an interrupted join job"},
					{Name: "timeout", Short: "maximum join duration"},
					{Name: "lease-ttl", Short: "join coordination lease duration"},
					{Name: "dry-run", Short: "print the plan without contacting the host"},
					{Name: "yes", Short: "approve the remote adoption"},
					{Name: "json", Short: "emit structured JSON"},
				},
			},
			{
				Name:  "add-node",
				Short: "Add a node to the fleet: write host_vars + hosts.ini + git commit + ssh bootstrap + POST compute_nodes",
				Flags: []cliFlag{
					{Name: "role", Short: "control-plane or compute-only (default: compute-only)"},
					{Name: "ansible-host", Short: "cross-box dial target (required)"},
					{Name: "public-iface", Short: "nftables substitution iface (compute-only only)"},
					{Name: "masquerade-cidr", Short: "per-host overlay CIDR (compute-only only)"},
					{Name: "masquerade-cidr-v6", Short: "ULA pool (compute-only only)"},
					{Name: "overlay-cidrs", Short: "comma-separated multi-host mesh entries (compute-only only)"},
					{Name: "target-url", Short: "compute_nodes target_url (compute-only only)"},
					{Name: "vpcpus", Short: "compute_nodes row vCPU count (compute-only only)"},
					{Name: "mem-mb", Short: "compute_nodes row RAM MB (compute-only only)"},
					{Name: "max-concurrency", Short: "compute_nodes row max concurrent live instances (compute-only only)"},
					{Name: "admission-ceiling-mb", Short: "compute_nodes row tenant RAM admission ceiling (compute-only only)"},
					{Name: "ssh", Short: "SSH target for bootstrap (default: gregale@<fqdn>)"},
					{Name: "skip-bootstrap", Short: "write host_vars + commit but do not SSH bootstrap"},
					{Name: "skip-compute-nodes-post", Short: "do not POST the compute_nodes row after bootstrap"},
					{Name: "repo-root", Short: "path to the cloned faas repo (default: ascend two levels)"},
					{Name: "yes", Short: "skip the pre-flight confirmation prompt"},
					{Name: "json", Short: "emit structured JSON to stdout"},
				},
			},
		},
	},
	{
		// Operator recovery primitives use the same authenticated apid
		// session + durable intent receipts as compute-node lifecycle.
		// Direct database/schedd access is break-glass only and reuses
		// the mTLS routing material installed for meterd.
		Name:    dispatchInstances,
		DocSlug: "instances",
		Short:   "Authenticated instance recovery (force-park|force-cold-boot|force-restart)",
		Subcommands: []cliSub{
			{
				Name:  "force-park",
				Short: "Submit and poll a durable intent to park a wedged live instance",
				Flags: []cliFlag{
					{Name: "instance-id", Short: "instance id (uuid) to force-park (required)"},
					{Name: "reason", Short: "durable audit reason slug [a-z0-9_]{1,64}"},
					{Name: "timeout", Short: "intent polling timeout"},
					{Name: "trace-id", Short: "OTel trace id (generated when omitted)"},
					{Name: "break-glass-local", Short: "bypass apid and call the local schedd"},
					{Name: "yes", Short: "acknowledge that the instance will be evicted from the wake path (required)"},
				},
			},
			{
				Name:  "force-cold-boot",
				Short: "Submit and poll a durable intent to stale an app's latest snapshots",
				Flags: []cliFlag{
					{Name: "app-slug", Short: "app slug whose latest deployment will be cold-booted on next wake (required)"},
					{Name: "reason", Short: "durable audit reason slug [a-z0-9_]{1,64}"},
					{Name: "timeout", Short: "intent polling timeout"},
					{Name: "trace-id", Short: "OTel trace id (generated when omitted)"},
					{Name: "break-glass-local", Short: "bypass apid and use the local database and schedd"},
					{Name: "yes", Short: "acknowledge that the customer's next wake will be a cold boot (required)"},
				},
			},
			{
				Name:  "force-restart",
				Short: "Submit and poll a durable intent to restart a wedged live instance",
				Flags: []cliFlag{
					{Name: "instance-id", Short: "instance id (uuid) to force-restart (required)"},
					{Name: "reason", Short: "durable audit reason slug [a-z0-9_]{1,64}"},
					{Name: "timeout", Short: "intent polling timeout"},
					{Name: "trace-id", Short: "OTel trace id (generated when omitted)"},
					{Name: "break-glass-local", Short: "bypass apid and call the local schedd"},
					{Name: "yes", Short: "acknowledge the kill and next-wake cold boot (required)"},
				},
			},
		},
	},
	{
		Name:    dispatchAccounts,
		DocSlug: "accounts",
		Short:   "Authenticated tenant support and lifecycle controls",
		Subcommands: []cliSub{
			{
				Name:  "list",
				Short: "List bounded, PII-redacted tenant summaries",
				Flags: []cliFlag{
					{Name: "limit", Short: "maximum accounts to return (1..500)"},
					{Name: "cursor", Short: "pagination cursor"},
					{Name: "plan", Short: "filter by plan"},
					{Name: "status", Short: "filter by account status"},
					{Name: "include-pii", Short: "include account email (audited)"},
				},
			},
			{
				Name:  "show",
				Short: "Show one account's apps, orgs, API keys, and sessions",
				Flags: []cliFlag{
					{Name: "account-id", Short: "account id (uuid)", Req: true},
					{Name: "include-pii", Short: "include account email (audited)"},
				},
			},
			{
				Name:  "360",
				Short: "Show one account's usage and billing support view",
				Flags: []cliFlag{
					{Name: "account-id", Short: "account id (uuid)", Req: true},
					{Name: "month", Short: "usage month (YYYY-MM)"},
					{Name: "include-pii", Short: "include account email (audited)"},
				},
			},
			{
				Name:  "activity",
				Short: "Show bounded invocation and audit metadata for an account",
				Flags: []cliFlag{
					{Name: "account-id", Short: "account id (uuid)", Req: true},
					{Name: "limit", Short: "maximum invocations and audit events (1..200)"},
				},
			},
			{
				Name:  "suspend",
				Short: "Suspend an account and revoke its active sessions",
				Flags: accountMutationCLIFlags(),
			},
			{
				Name:  "restore",
				Short: "Restore an account to active status",
				Flags: accountMutationCLIFlags(),
			},
			{
				Name:  "revoke-sessions",
				Short: "Revoke every active session for an account",
				Flags: accountMutationCLIFlags(),
			},
		},
	},
	{
		Name:    dispatchConfig,
		DocSlug: "config",
		Short:   "Inspect and safely change hot runtime configuration",
		Subcommands: []cliSub{
			{Name: "list", Short: "List desired and effective runtime configuration"},
			{Name: "show", Short: "Show one runtime configuration entry", Flags: []cliFlag{
				{Name: "key", Short: "runtime configuration key", Req: true},
			}},
			{Name: "history", Short: "Show append-only revisions for one key", Flags: []cliFlag{
				{Name: "key", Short: "runtime configuration key", Req: true},
				{Name: "limit", Short: "maximum revisions to return (1..200)"},
			}},
			{Name: "set", Short: "Change a hot setting without a rollout", Flags: configMutationCLIFlags(true)},
			{Name: "rollback", Short: "Restore an older hot-setting revision", Flags: configMutationCLIFlags(false)},
		},
	},
	{
		Name:    dispatchFlags,
		DocSlug: "flags",
		Short:   "Release platform features to selected accounts (ADR-1700)",
		Subcommands: []cliSub{
			{Name: "list", Short: "List platform flags, groups and per-daemon convergence"},
			{Name: "show", Short: "Show one flag's definition and apply receipts", Flags: []cliFlag{
				{Name: "key", Short: "platform flag key", Req: true},
			}},
			{Name: "inspect", Short: "Explain one flag's decision for an account", Flags: []cliFlag{
				{Name: "key", Short: "platform flag key", Req: true},
				{Name: "account-id", Short: "account UUID", Req: true},
				{Name: "org-id", Short: "organization UUID the request would act under"},
				{Name: "at", Short: "decide as of this RFC 3339 time"},
			}},
			{Name: "account", Short: "Show every flag decided for an account", Flags: []cliFlag{
				{Name: "account-id", Short: "account UUID", Req: true},
			}},
			{Name: "enable", Short: "Turn a flag's kill switch on", Flags: flagsMutationCLIFlags(true)},
			{Name: "disable", Short: "Turn a flag off for every account", Flags: flagsMutationCLIFlags(true)},
			{Name: "allow", Short: "Enable a flag for one account or organization", Flags: append(flagsMutationCLIFlags(true), flagsSubjectCLIFlags()...)},
			{Name: "deny", Short: "Exclude one account or organization from a flag", Flags: append(flagsMutationCLIFlags(true), flagsSubjectCLIFlags()...)},
			{Name: "revoke", Short: "Remove an account's or organization's explicit allow or deny", Flags: append(flagsMutationCLIFlags(true), flagsSubjectCLIFlags()...)},
			{Name: "rollout", Short: "Release a flag to a stable share of accounts", Flags: append(flagsMutationCLIFlags(true),
				cliFlag{Name: "percent", Short: "share of eligible accounts, 0..100", Req: true},
				cliFlag{Name: "plans", Short: "comma-separated plans the rollout is limited to"})},
			{Name: "default", Short: "Set the value for accounts no rule matches", Flags: append(flagsMutationCLIFlags(true), cliFlag{Name: "value", Short: "true or false", Req: true})},
			{Name: "schedule", Short: "Set or clear the time window of one rule", Flags: append(flagsMutationCLIFlags(true),
				cliFlag{Name: "rule-id", Short: "rule to schedule", Req: true},
				cliFlag{Name: "starts-at", Short: "RFC 3339 start, or none to clear"},
				cliFlag{Name: "ends-at", Short: "RFC 3339 end, or none to clear"})},
			{Name: "ramp", Short: "Advance the rollout automatically while the flag stays healthy", Flags: append(flagsMutationCLIFlags(true),
				cliFlag{Name: "stages", Short: "comma-separated rollout percentages, including the current one"},
				cliFlag{Name: "interval", Short: "how long each stage holds before it is judged (>= 5m)"},
				cliFlag{Name: "max-5xx-percent", Short: "highest 5xx share of the health routes that still advances"},
				cliFlag{Name: "min-requests", Short: "requests a stage needs before it is judged"},
				cliFlag{Name: "on-breach", Short: "pause or rollback"},
				cliFlag{Name: "resume", Short: "clear a paused ramp"},
				cliFlag{Name: "remove", Short: "remove the ramp, keep the rollout"})},
			{Name: "self-serve", Short: "Open or close a preview to customer opt-ins", Flags: append(flagsMutationCLIFlags(true),
				cliFlag{Name: "enabled", Short: "true opens the preview to opt-ins; false ignores existing ones", Req: true},
				cliFlag{Name: "plans", Short: "comma-separated eligible plans, or all; empty keeps the current list"},
				cliFlag{Name: "max-subjects", Short: "cap on opted-in accounts plus organizations (0 = 5000); -1 keeps it"})},
			{Name: "set", Short: "Replace a flag's definition from a JSON file", Flags: append(flagsMutationCLIFlags(true), cliFlag{Name: "file", Short: "JSON definition file, or - for stdin", Req: true})},
			{Name: "groups", Short: "List account groups and the flags that use them"},
			{Name: "group-add", Short: "Add an account to a group", Flags: append(flagsMutationCLIFlags(false),
				cliFlag{Name: "group", Short: "group name", Req: true}, cliFlag{Name: "account-id", Short: "account UUID", Req: true})},
			{Name: "group-remove", Short: "Remove an account from a group", Flags: append(flagsMutationCLIFlags(false),
				cliFlag{Name: "group", Short: "group name", Req: true}, cliFlag{Name: "account-id", Short: "account UUID", Req: true})},
			{Name: "history", Short: "Show a flag's or the groups' revisions", Flags: []cliFlag{
				{Name: "key", Short: "platform flag key, or groups", Req: true},
				{Name: "limit", Short: "maximum revisions to return (1..200)"},
			}},
			{Name: "rollback", Short: "Restore an older flag or groups revision", Flags: append(flagsMutationCLIFlags(true), cliFlag{Name: "version", Short: "historical version to restore", Req: true})},
		},
	},
	{
		Name:    dispatchAudit,
		DocSlug: "audit",
		Short:   "Correlate operator intents by trace ID; audit app layers before a base change",
		Subcommands: []cliSub{{
			Name:  "trace",
			Short: "Look up one exact OTel trace ID through the operator API",
			Flags: []cliFlag{
				{Name: "trace-id", Short: "OTel 32-char lowercase hex trace id", Req: true},
				{Name: "limit", Short: "maximum intents and events to return (1..500)"},
			},
		}, {
			// ADR 20261023220532 rollout step 4: read-only; reads PostgreSQL
			// and the layer store directly from a control-plane node.
			Name:  auditBaseLibrariesSub,
			Short: "List live base-minimal app layers that need a library the Debian 13 base drops",
			Flags: []cliFlag{
				{Name: "pg-dsn", Short: "PostgreSQL DSN (default: $FAAS_PG_DSN or $DATABASE_URL)"},
				{Name: "db-env", Short: "systemd env file containing DATABASE_URL"},
				{Name: "app", Short: "audit only this app slug"},
				{Name: "include-superseded", Short: "also audit superseded deployments (rollback targets)"},
				{Name: "work-dir", Short: "scratch directory for layer copies and dumps"},
				{Name: "debugfs", Short: "debugfs binary (e2fsprogs)"},
				{Name: "libraries", Short: "comma-separated sonames the new base drops"},
			},
		}},
	},
	{
		// Authenticated build recovery through the existing apid
		// mutation surface. No direct database credential is used.
		Name:    dispatchBuilds,
		DocSlug: "builds",
		Short:   "Build-recovery primitives (builds sweep-stuck)",
		Subcommands: []cliSub{
			{
				Name:  "sweep-stuck",
				Short: "Flip every 'running' build row older than the threshold to 'failed/timeout'",
				Flags: []cliFlag{
					{Name: "older-than", Short: "threshold duration (1m..60m; default 15m)"},
					{Name: "reason", Short: "durable audit reason slug [a-z0-9_]{1,64}", Req: true},
					{Name: "trace-id", Short: "OTel trace id (generated when omitted)"},
					{Name: "yes", Short: "acknowledge that rows older than the threshold will be flipped (required)"},
				},
			},
		},
	},
	{
		Name:    dispatchJobs,
		DocSlug: "jobs",
		Short:   "Cross-account job-run diagnosis and guarded cancellation",
		Subcommands: []cliSub{
			{Name: "active", Short: "List queued and running job runs across the fleet or for one account", Flags: []cliFlag{
				{Name: "account-id", Short: "optional target account id; omit for a bounded fleet view"},
				{Name: "limit", Short: "maximum active runs (1..500)"},
				{Name: "offset", Short: "active-run pagination offset"},
			}},
			{Name: "inspect", Short: "Inspect one run and a bounded task page", Flags: []cliFlag{
				{Name: "run-id", Short: "job run id", Req: true},
				{Name: "task-limit", Short: "maximum tasks (1..500)"},
				{Name: "task-offset", Short: "task pagination offset"},
			}},
			{Name: "cancel", Short: "Cancel every non-terminal task in one job run", Flags: []cliFlag{
				{Name: "run-id", Short: "job run id", Req: true},
				{Name: "reason", Short: "durable audit reason slug", Req: true},
				{Name: "trace-id", Short: "OTel trace id (generated when omitted)"},
				{Name: "yes", Short: "acknowledge cancelling customer job work", Req: true},
			}},
		},
	},
	{
		Name:    dispatchDeployments,
		DocSlug: "deployments",
		Short:   "Cross-account deployment diagnosis and guarded retry/cancellation",
		Subcommands: []cliSub{
			{Name: "active", Short: "List incident-state deployments across the fleet or for one account/app", Flags: []cliFlag{
				{Name: "account-id", Short: "optional target account id"},
				{Name: "app-id", Short: "optional target app id"},
				{Name: "status", Short: "comma-separated status filter; default incident states; all for every state"},
				{Name: "limit", Short: "maximum deployments (1..200)"},
				{Name: "offset", Short: "deployment pagination offset"},
			}},
			{Name: "inspect", Short: "Inspect one deployment, build, and bounded stage timeline", Flags: []cliFlag{
				{Name: "deployment-id", Short: "deployment id", Req: true},
			}},
			{Name: "cancel", Short: "Cancel one pending deployment and its active build", Flags: []cliFlag{
				{Name: "deployment-id", Short: "deployment id", Req: true},
				{Name: "reason", Short: "durable audit reason slug", Req: true},
				{Name: "trace-id", Short: "OTel trace id (generated when omitted)"},
				{Name: "yes", Short: "acknowledge cancelling customer deployment work", Req: true},
			}},
			{Name: "retry", Short: "Enqueue a failed deployment from a selected stage", Flags: []cliFlag{
				{Name: "deployment-id", Short: "failed deployment id", Req: true},
				{Name: "from-stage", Short: "stage to restart from (default source_download)"},
				{Name: "reason", Short: "durable audit reason slug", Req: true},
				{Name: "trace-id", Short: "OTel trace id (generated when omitted)"},
				{Name: "yes", Short: "acknowledge enqueueing customer deployment work", Req: true},
			}},
			{Name: "repair-stale", Short: "Dry-run or cancel stale nonterminal deployments without active builds", Flags: []cliFlag{
				{Name: "older-than", Short: "minimum deployment age (default 2h)"},
				{Name: "limit", Short: "maximum deployments (1..200)"},
				{Name: "yes", Short: "apply cancellation; omit for dry-run"},
			}},
		},
	},
	{
		Name:    "manifest",
		DocSlug: "manifest",
		Short:   "Operator split-box deployment manifest (manifest validate|render|ansible; issue #911 / ADR-110)",
		Subcommands: []cliSub{
			{
				Name:  subValidate,
				Short: "Validate a manifest YAML file (canonical path: pkg/manifest.Validate)",
				Flags: []cliFlag{{Name: "file", Short: "path to the manifest YAML file (required)"}},
			},
			{
				Name:  subRender,
				Short: "Render a validated manifest to /etc/faas/*.toml + systemd units + cgroup subtree_control + PKI leaves (canonical path: pkg/renderer.Render)",
				Flags: []cliFlag{
					{Name: "manifest-file", Short: "path to the manifest YAML file (required)", Req: true},
					{Name: "host", Short: "host in the manifest to render (default: first host)"},
					{Name: "releases-root", Short: "releases root (default /opt/faas/releases)"},
					{Name: "etc-faas-dir", Short: "TOML root (default /etc/faas)"},
					{Name: "systemd-dir", Short: "systemd unit tree (default /etc/systemd/system)"},
					{Name: "pki-root-dir", Short: "PKI root (default /etc/faas/tls)"},
					{Name: "cgroup-root", Short: "cgroup v2 mount root (default /sys/fs/cgroup)"},
					{Name: "host-san-file", Short: "optional JSON file with per-host SANs"},
					{Name: "pki-trust-only", Short: "validate existing leaves without the CA private key"},
					{Name: "dry-run", Short: "compute outputs but do not write"},
				},
			},
			{
				Name:  subAnsible,
				Short: "Generate Ansible inventory + host_vars tree from the validated manifest (canonical path: pkg/manifest.AnsibleRender; consumed by `make manifest-ansible` and the deployctl bootstrap)",
				Flags: []cliFlag{
					{Name: "manifest-file", Short: "path to the manifest YAML file (required)", Req: true},
					{Name: "output-dir", Short: "directory to write inventory + host_vars/ under (default: deploy/ansible/.generated/)"},
					{Name: "force", Short: "overwrite existing files under --output-dir (refuse by default — re-running on a dirty tree is operator error)"},
					{Name: "dry-run", Short: "compute outputs but do not write"},
					{Name: "json", Short: "emit structured JSON to stdout"},
				},
			},
		},
	},
	{
		Name:    "release",
		DocSlug: "release",
		Short:   "Cluster-shipped release bundle (release bundle|install|kgv|reconcile; PR-3 / ADR-110, PR-B / ADR-113)",
		Subcommands: []cliSub{
			{
				Name:  subReleaseBundle,
				Short: "Materialise a release bundle from a pre-built bin directory and INSERT into release_bundles",
				Flags: []cliFlag{
					{Name: "bin-dir", Short: "path to daemon binaries directory (required)", Req: true},
					{Name: "git-sha", Short: "40-char lowercase hex git SHA (required)", Req: true},
					{Name: "manifest-hash", Short: "manifest hash as 'sha256:<64hex>' (required)", Req: true},
					{Name: "releases-root", Short: "releases root (default /opt/faas/releases)"},
				},
			},
			{
				Name:  subReleaseInstall,
				Short: "Install a release on the local box (atomic symlink flip + applied_at first-write-wins stamp + compute_nodes UPSERT). --role is the dual-purpose flag: on first-boot it templates drop-ins and normally starts the role subset; --defer-activation leaves a new compute subset stopped for the readiness pipeline. On a running box with a different existing role it triggers PR-B in-place mutation (drain-gate, Mutate(stop+start), role UPSERT).",
				Flags: []cliFlag{
					{Name: "git-sha", Short: "40-char lowercase hex git SHA to install (required)", Req: true},
					{Name: "releases-root", Short: "releases root (default /opt/faas/releases)"},
					{Name: "node", Short: "compute_nodes.name to stamp (default: FAAS_NODE_NAME, then hostname; compute-only uses NAME.faas)"},
					{Name: "role", Short: "box role: control-plane|compute-only (ADR-112); empty = no role templating. Reads /etc/faas/first-boot.env's FAAS_BOX_ROLE when unset.", ClosedSet: []string{"", "control-plane", "compute-only"}},
					{Name: "defer-activation", Short: "keep a compute row drained until readiness gates pass; on first boot, leave daemons stopped for the join pipeline"},
				},
			},
			{
				// PR-B (ADR-113 day-2): operator escape hatch from the
				// fail-closed SBoM CVE-baseline gate. The KGV is the
				// "known good version" baseline the install path compares
				// against; rotate re-stamps it from the on-disk release
				// SBoM (or KGVZero with --from-zero). CD verifies the
				// candidate before activation and accepts it only after
				// all host health gates pass.
				Name:  subReleaseKGV,
				Short: "Verify a candidate SBoM against the canonical accepted baseline, or accept an activated release with release kgv rotate",
				Flags: []cliFlag{
					{Name: "git-sha", Short: "40-char lowercase hex git SHA (required)", Req: true},
					{Name: "releases-root", Short: "releases root (default /opt/faas/releases)"},
					{Name: "from-zero", Short: "write KGVZero (zero CRITICAL/HIGH) without parsing the on-disk SBoM"},
					{Name: "json", Short: "emit structured JSON to stdout"},
				},
			},
			{
				Name:  subReleaseReconcile,
				Short: "Remove stale unapplied release rows after a newer release succeeds",
				Flags: []cliFlag{
					{Name: "releases-root", Short: "releases root (default /opt/faas/releases)"},
					{Name: "retention", Short: "minimum abandoned-row age (default 24h)"},
					{Name: "dry-run", Short: "report eligible rows without deleting"},
					{Name: "json", Short: "emit structured JSON to stdout"},
				},
			},
		},
	},
	{
		// PR-4 (issue #911 / ADR-110): read-only cluster diagnostic.
		// Walks the on-disk release tree + the release_bundles +
		// compute_nodes tables and reports drift. NEVER writes.
		Name:    dispatchDoctor,
		DocSlug: "doctor",
		Short:   "Read-only diagnostic for the cluster-shipped release bundle (doctor [--node NAME] [--release SHA] [--deep]; PR-4 / ADR-110)",
		Flags: []cliFlag{
			{Name: "node", Short: "compute_nodes.name filter (default: all)"},
			{Name: "release", Short: "release_bundles.git_sha filter (default: all)"},
			{Name: "releases-root", Short: "releases root (default /opt/faas/releases)"},
			{Name: "deep", Short: "re-hash on-disk daemons per-node (slow on large fleets)"},
			{Name: "fail-on", Short: "exit non-zero threshold: warn | error (default error)", ClosedSet: []string{"warn", "error"}},
		},
	},
	{
		Name:    dispatchPKI,
		DocSlug: "pki",
		Short:   "Operator PKI bootstrap and scheduled host-bundle renewal",
		Subcommands: []cliSub{
			{Name: subPKIInit, Short: "Initialise the local PKI"},
			{Name: subPKIStatus, Short: "Show PKI status"},
			{Name: subPKIList, Short: "List PKI leaves + CA (--json; --daemon NAME; --box-role ROLE)"},
			{Name: subPKIRotate, Short: "Rotate the PKI"},
			{
				Name:  subPKIExportBundle,
				Short: "Export a validated trust-only active bundle",
				Flags: []cliFlag{
					{Name: "source-root", Short: "active trust root"},
					{Name: "output-dir", Short: "fresh trust-only export destination", Req: true},
					{Name: "box-role", Short: "target host role", Req: true, ClosedSet: []string{"control-plane", "compute-only"}},
					{Name: "cn", Short: "compute node identity"},
					{Name: "transport-san", Short: "target node transport DNS name or IP"},
				},
			},
			{
				Name:  subPKIIssueBundle,
				Short: "Issue a node-scoped trust-only renewal bundle",
				Flags: []cliFlag{
					{Name: "issuer-root", Short: "operator PKI root containing the CA private key", Req: true},
					{Name: "active-root", Short: "exported active bundle whose safe leaves are preserved"},
					{Name: "output-dir", Short: "trust-only bundle destination", Req: true},
					{Name: "changed-file", Short: "JSON list of renewed leaf roles"},
					{Name: "box-role", Short: "target host role", Req: true, ClosedSet: []string{"control-plane", "compute-only"}},
					{Name: "cn", Short: "compute node identity"},
					{Name: "transport-san", Short: "target node transport DNS name or IP"},
				},
			},
			{
				Name:  subPKIFingerprint,
				Short: "Print the canonical SHA-256 certificate fingerprint",
				Flags: []cliFlag{
					{Name: "cert", Short: "certificate path", Req: true},
				},
			},
			{
				Name:  subPKIInstallBundle,
				Short: "Transactionally install a node-scoped renewal bundle",
				Flags: []cliFlag{
					{Name: "bundle-dir", Short: "candidate trust-only bundle", Req: true},
					{Name: "root-dir", Short: "active PKI root"},
					{Name: "box-role", Short: "target host role", Req: true, ClosedSet: []string{"control-plane", "compute-only"}},
					{Name: "cn", Short: "compute node identity"},
					{Name: "transport-san", Short: "target node transport DNS name or IP"},
				},
			},
			{
				Name:  subPKIRecoverInstall,
				Short: "Recover an interrupted trust bundle installation",
				Flags: []cliFlag{{Name: "root-dir", Short: "active PKI root"}},
			},
			{
				Name:  subPKIIssueFeature,
				Short: "Issue or renew an operator-enabled feature's leaves (live-queries) from the CA",
				Flags: []cliFlag{
					{Name: "feature", Short: "feature whose leaves to issue", Req: true, ClosedSet: []string{"live-queries"}},
					{Name: "box-role", Short: "host role the leaves are for", Req: true, ClosedSet: []string{"control-plane", "compute-only", "single-box"}},
					{Name: "ca-dir", Short: "PKI root holding the CA"},
					{Name: "root-dir", Short: "PKI root the leaves are written under"},
				},
			},
			{
				Name:  subPKIMetrics,
				Short: "Export active leaf expiry and renewal state for node_exporter",
				Flags: []cliFlag{
					{Name: "root-dir", Short: "active PKI root"},
					{Name: "box-role", Short: "host role", Req: true, ClosedSet: []string{"control-plane", "compute-only"}},
					{Name: "host", Short: "bounded host label", Req: true},
					{Name: "output", Short: "node_exporter textfile destination"},
					{Name: "renewal-state", Short: "scheduled renewal state JSON"},
				},
			},
		},
	},
	{
		Name:    dispatchSignKeys,
		DocSlug: "sign-keys",
		Short:   "Provision the cosign sign keypair (operator; --sign-key / --verify-key)",
		Subcommands: []cliSub{
			{Name: subInit, Short: "Initialise the cosign keypair"},
			{Name: subRotate, Short: "Rotate the cosign keypair"},
			{Name: subStatus, Short: "Show keypair status"},
		},
		Flags: []cliFlag{
			{Name: "sign-key", Short: "path to the sign key"},
			{Name: "verify-key", Short: "path to the verify key"},
		},
	},
	{
		Name:    dispatchNodeKey,
		DocSlug: "node-key",
		Short:   "Provision the per-node CapacityReport signing keypair (operator; ADR-053)",
		Subcommands: []cliSub{
			{Name: subNodeInit, Short: "Initialise the node signing keypair"},
			{Name: subNodeRotate, Short: "Rotate the node signing keypair"},
			{Name: subNodeStatus, Short: "Show node keypair status"},
		},
		Flags: []cliFlag{
			{Name: "node-key", Short: "path to the node signing private key"},
			{Name: "node-key-pub", Short: "path to the node signing public key"},
		},
	},
	{
		Name:    dispatchWorkloadIdentity,
		DocSlug: "workload-identity",
		Short:   "Provision the cluster workload-identity signing key and JWKS (operator; ADR-1141)",
		Subcommands: []cliSub{
			{Name: subInit, Short: "Write a new key, key id and JWKS"},
			{Name: subRotate, Short: "Rotate the key; the JWKS keeps the previous public key"},
			{Name: subStatus, Short: "Show the key id and check the kid file and JWKS match"},
		},
		Flags: []cliFlag{
			{Name: "out", Short: "directory holding workload-identity.{key,kid,jwks.json}"},
		},
	},
	{
		// PR-X (issue #911 / ADR-110): post-bootstrap secrets
		// initialisation. Replaces v1 bootstrap.sh step 11d
		// (RETIRED 2026-08-15). Writes 5 secret files in one
		// batch and stamps compute_nodes.{host_certificate,
		// cert_fingerprint}.
		Name:    dispatchSecrets,
		DocSlug: "secrets",
		Short:   "Post-bootstrap secrets init (secrets init|rotate|status|stamp; PR-X / issue #911 / ADR-110)",
		Subcommands: []cliSub{
			{Name: subInit, Short: "Initialise the 5 on-disk secrets (host.age, session.key, box-age-key, rclone.conf, archive-creds.json)"},
			{Name: subRotate, Short: "Rotate host.age (delegates to host-age rotate)"},
			{Name: subStatus, Short: "Show mode/mtime/sha256 for the 5 secret files"},
			{Name: subSecretsStamp, Short: "Stamp the existing vmmd TLS certificate without rotating secrets"},
		},
		Flags: []cliFlag{
			{Name: "dir", Short: "root secrets directory (default /etc/faas/secrets)"},
			{Name: "host", Short: "compute_nodes.name to stamp (default: hostname)"},
			{Name: "role", Short: "compute_nodes.role to stamp (default: empty)"},
			{Name: "pg-dsn", Short: "PostgreSQL DSN (default: $FAAS_PG_DSN or $DATABASE_URL)"},
			{Name: "expected-fingerprint", Short: "CAS guard for certificate attestation rotation"},
			{Name: "no-db", Short: "skip the compute_nodes.cert_fingerprint write"},
			{Name: "force", Short: "overwrite existing secret files (default false)"},
		},
	},
	{
		// Operator reads and episode-scoped triage. Mutations use the strict
		// operator session policy; other reads retain admin bearer support.
		Name:    dispatchObs,
		DocSlug: "obs",
		Short:   "Operator incident inbox, health, fleet overview, and capacity",
		Subcommands: []cliSub{
			{
				Name:  subObsHealth,
				Short: "Fetch GET /v1/admin/obs/health from apid (admin scope + MFA required)",
				Flags: []cliFlag{
					{Name: "json", Short: "emit raw JSON snapshot (overrides human summary)"},
					{Name: "admin-token", Short: "admin bearer for the FAAS_ADMIN_EMAILS allowlist (default: $FAAS_ADMIN_TOKEN)"},
					{Name: "timeout", Short: "HTTP timeout for the apid round-trip (default 10s)"},
				},
			},
			{
				Name:  subObsNode,
				Short: "One compute node's support page: admitted RAM, heartbeats, wake latency, drain safety, apps, instances",
				Flags: []cliFlag{
					{Name: "heartbeats", Short: "recent heartbeats to read, 1-100 (default 20)"},
					{Name: "instances", Short: "live instances to list, 0-100 (default 20)"},
					{Name: "admin-token", Short: "admin bearer (default: $FAAS_ADMIN_TOKEN)"},
					{Name: "timeout", Short: "HTTP timeout per request (default 30s)"},
					{Name: "json", Short: "emit structured JSON"},
				},
			},
			{
				Name:  subObsApp,
				Short: "One app's support page: live deployment, next-wake path, instances, health, wakes, errors",
				Flags: []cliFlag{
					{Name: "range", Short: "health window: 5m|15m|1h|6h|24h|7d|15d (default 1h)"},
					{Name: "wakes", Short: "recent wakes to list, 0-100 (default 10)"},
					{Name: "admin-token", Short: "admin bearer (default: $FAAS_ADMIN_TOKEN)"},
					{Name: "timeout", Short: "HTTP timeout (default 30s)"},
					{Name: "json", Short: "emit the full response as JSON"},
				},
			},
			{
				Name:  subObsWake,
				Short: "One wake's platform events and log lines from every host, as one timeline",
				Flags: []cliFlag{
					{Name: "no-logs", Short: "events only; do not query Loki"},
					{Name: "log-margin", Short: "how far around the first and last event to search the logs (default 2m)"},
					{Name: "admin-token", Short: "admin bearer (default: $FAAS_ADMIN_TOKEN)"},
					{Name: "timeout", Short: "HTTP timeout for apid and Loki (default 30s)"},
					{Name: "json", Short: "emit structured JSON"},
				},
			},
			{
				Name:  subObsLogs,
				Short: "Search platform logs in Loki by wake, request, trace, host, unit or level",
				Flags: []cliFlag{
					{Name: "wake-id", Short: "lines that mention this wake_id"},
					{Name: "request-id", Short: "lines that mention this request_id"},
					{Name: "trace-id", Short: "lines that mention this trace_id"},
					{Name: "host", Short: "only this host (e.g. fsn-3)"},
					{Name: "unit", Short: "only this unit (schedd or faas-schedd.service)"},
					{Name: "level", Short: "only debug|info|warn|error"},
					{Name: "app", Short: "only customer app-log records of this app_id"},
					{Name: "account", Short: "only customer app-log records of this account_id"},
					{Name: "grep", Short: "only lines containing this text"},
					{Name: "query", Short: "raw LogQL; replaces every filter flag"},
					{Name: "since", Short: "how far back from --to (default 1h, maximum 14d)"},
					{Name: "from", Short: "RFC3339 start (overrides --since)"},
					{Name: "to", Short: "RFC3339 end (default now)"},
					{Name: "limit", Short: "maximum lines (1..5000; default 200)"},
					{Name: "forward", Short: "return the oldest lines in the range instead of the newest"},
					{Name: "loki-url", Short: "Loki base URL (default: $FAAS_LOKI_URL or /etc/faas/loki-client.env)"},
					{Name: "loki-server-name", Short: "name in the Loki server certificate"},
					{Name: "loki-tenant", Short: "Loki tenant (default gregale)"},
					{Name: "loki-ca", Short: "CA file that signed the Loki server certificate"},
					{Name: "loki-cert", Short: "client certificate file"},
					{Name: "loki-key", Short: "client key file"},
					{Name: "json", Short: "emit structured JSON"},
					{Name: "timeout", Short: "HTTP timeout (default 30s)"},
				},
			},
			{
				Name:  subObsIncidents,
				Short: "List bounded deployment, job, node, and alert incidents",
				Flags: []cliFlag{
					{Name: "type", Short: "filter by deployment|job_run|compute_node|platform_alert"},
					{Name: "severity", Short: "filter by warning|error|critical"},
					{Name: "alert-status", Short: "platform alerts: firing|resolved|unknown"},
					{Name: "acknowledgment", Short: "platform alerts: acknowledged|unacknowledged"},
					{Name: "ownership", Short: "platform alerts: owned|unowned|mine (mine uses operator session)"},
					{Name: "snooze", Short: "platform alerts: active|inactive"},
					{Name: "since", Short: "RFC 3339 lower bound (default last 24h; capped at 7d)"},
					{Name: "cursor", Short: "opaque next_cursor from a prior page"},
					{Name: "limit", Short: "maximum incidents (1..200; default 50)"},
					{Name: "json", Short: "emit structured JSON"},
					{Name: "admin-token", Short: "admin bearer (default: $FAAS_ADMIN_TOKEN)"},
					{Name: "timeout", Short: "HTTP timeout for the apid round-trip (default 10s)"},
				},
			},

			{
				Name: subObsResponseTimes, Short: "Measure retained episode acknowledgment and observed resolution", Flags: []cliFlag{{Name: "since", Short: "activation-cohort lower bound (RFC3339; default 24h, maximum 30d)"}, {Name: "admin-token", Short: "admin bearer (default: $FAAS_ADMIN_TOKEN)"}, {Name: "timeout", Short: "HTTP timeout (default 10s)"}, {Name: "json", Short: "emit structured JSON"}},
			},
			{
				Name: subObsRecurrence, Short: "Inspect retained alert repeats and observed recovery gaps", Flags: []cliFlag{{Name: "since", Short: "activation-cohort lower bound (RFC3339; default 24h, maximum 30d)"}, {Name: "admin-token", Short: "admin bearer (default: $FAAS_ADMIN_TOKEN)"}, {Name: "timeout", Short: "HTTP timeout (default 10s)"}, {Name: "json", Short: "emit structured JSON"}},
			},
			{
				Name: subObsCollectionHistory, Short: "Inspect prospective collection outcomes and unknown intervals", Flags: []cliFlag{{Name: "since", Short: "collection-history lower bound (RFC3339; default 24h, maximum 30d)"}, {Name: "details", Short: "include retained diagnostics for recent failed attempts"}, {Name: "admin-token", Short: "admin bearer (default: $FAAS_ADMIN_TOKEN)"}, {Name: "timeout", Short: "HTTP timeout (default 10s)"}, {Name: "json", Short: "emit structured JSON"}},
			},
			{
				Name: subObsNotificationHealth, Short: "Inspect alert notification pipeline evidence", Flags: []cliFlag{{Name: "admin-token", Short: "admin bearer (default: $FAAS_ADMIN_TOKEN)"}, {Name: "timeout", Short: "HTTP timeout (default 10s)"}, {Name: "json", Short: "emit structured JSON"}},
			},
			{
				Name: subObsRuleHealth, Short: "Inspect current alert-rule evaluation diagnostics", Flags: []cliFlag{{Name: "admin-token", Short: "admin bearer (default: $FAAS_ADMIN_TOKEN)"}, {Name: "timeout", Short: "HTTP timeout (default 10s)"}, {Name: "json", Short: "emit structured JSON"}},
			},
			{
				Name: subObsRuleCoverage, Short: "Compare explicitly selected expected alert definitions", Flags: []cliFlag{{Name: "expected-set", Short: "explicit expected set (unset means unknown)", ClosedSet: []string{"gregale-core-v1", "gregale-full-v1"}}, {Name: "admin-token", Short: "admin bearer (default: $FAAS_ADMIN_TOKEN)"}, {Name: "timeout", Short: "HTTP timeout (default 10s)"}, {Name: "json", Short: "emit structured JSON"}},
			},
			{
				Name: subObsRecordingCoverage, Short: "Compare expected recording rules and catalog alert references", Flags: []cliFlag{{Name: "expected-set", Short: "explicit expected set (unset means unknown)", ClosedSet: []string{"gregale-core-v1", "gregale-full-v1"}}, {Name: "admin-token", Short: "admin bearer (default: $FAAS_ADMIN_TOKEN)"}, {Name: "timeout", Short: "HTTP timeout (default 10s)"}, {Name: "json", Short: "emit structured JSON"}},
			}, {
				Name: subObsInputHealth, Short: "Inspect critical scrape and telemetry input health", Flags: []cliFlag{{Name: "expected-role", Short: "explicit role expectation (unset means unknown)", ClosedSet: []string{"single-box-v1", "control-plane-v1"}}, {Name: "admin-token", Short: "admin bearer (default: $FAAS_ADMIN_TOKEN)"}, {Name: "timeout", Short: "HTTP timeout (default 10s)"}, {Name: "json", Short: "emit structured JSON"}},
			},
			{
				Name:  subObsSummary,
				Short: "Summarize all retained platform alert workload and freshness",
				Flags: []cliFlag{{Name: "admin-token", Short: "admin bearer (default: $FAAS_ADMIN_TOKEN)"}, {Name: "timeout", Short: "HTTP timeout (default 10s)"}, {Name: "json", Short: "emit structured JSON"}},
			},
			{
				Name:  subObsTriageHistory,
				Short: "Read retained episode triage actions and receipt coverage",
				Flags: []cliFlag{
					{Name: "incident", Short: "platform_alert/episode-id (legacy series aliases resolve on the first page)"},
					{Name: "limit", Short: "maximum receipts (1..100; default 25)"},
					{Name: "cursor", Short: "opaque next_cursor from this episode; expires after 15 minutes"},
					{Name: "admin-token", Short: "admin bearer (default: $FAAS_ADMIN_TOKEN)"},
					{Name: "timeout", Short: "HTTP timeout (default 10s)"},
					{Name: "json", Short: "emit structured JSON"},
				},
			},
			{
				Name:  subObsTriage,
				Short: "Acknowledge, claim, or snooze a retained alert episode (operator session + fresh MFA)",
				Flags: []cliFlag{
					{Name: "incident", Short: "immutable platform_alert/episode-id"},
					{Name: "action", Short: "acknowledge|unacknowledge|claim|release|snooze|unsnooze"},
					{Name: "expected-revision", Short: "revision from evidence (required; 0 for untouched episodes)"},
					{Name: "until", Short: "RFC 3339 snooze deadline within 24 hours"},
					{Name: "json", Short: "emit structured JSON"},
					{Name: "timeout", Short: "total request timeout (default 10s)"},
				},
			},
			{
				Name:  subObsBundle,
				Short: "Export sanitized incident evidence, related context, and source health",
				Flags: []cliFlag{
					{Name: "incident", Short: "inbox incident ID: type/resource-id"},
					{Name: "output", Short: "new owner-only JSON file (required; - for stdout)"},
					{Name: "limit", Short: "maximum related resources (1..100; default 25)"},
					{Name: "history-limit", Short: "maximum entries per evidence/timeline section (1..100; default 25)"},
					{Name: "json", Short: "emit JSON file receipt; stdout exports always contain the bundle"},
					{Name: "admin-token", Short: "admin bearer (default: $FAAS_ADMIN_TOKEN)"},
					{Name: "timeout", Short: "total collection timeout (default 30s; maximum 1m)"},
				},
			},
			{
				Name:  subObsContext,
				Short: "Inspect current incident exposure and related evidence",
				Flags: []cliFlag{
					{Name: "incident", Short: "inbox incident ID: type/resource-id"},
					{Name: "limit", Short: "maximum related resources (1..100; default 25)"},
					{Name: "history-limit", Short: "maximum related timeline entries (1..100; default 25)"},
					{Name: "cursor", Short: "opaque next_cursor from this incident; expires after 15 minutes"},
					{Name: "json", Short: "emit structured JSON"},
					{Name: "admin-token", Short: "admin bearer (default: $FAAS_ADMIN_TOKEN)"},
					{Name: "timeout", Short: "HTTP timeout (default 10s)"},
				},
			},
			{
				Name:  subObsEvidence,
				Short: "Inspect current resource state and bounded incident history",
				Flags: []cliFlag{
					{Name: "incident", Short: "inbox incident ID: type/resource-id"},
					{Name: "limit", Short: "maximum history entries (1..100; default 25)"},
					{Name: "json", Short: "emit structured JSON"},
					{Name: "admin-token", Short: "admin bearer (default: $FAAS_ADMIN_TOKEN)"},
					{Name: "timeout", Short: "HTTP timeout (default 10s)"},
				},
			},
			{
				Name:  subObsOverview,
				Short: "Fetch GET /v1/admin/obs/overview (admin scope + MFA required)",
				Flags: []cliFlag{
					{Name: "json", Short: "emit structured JSON"},
					{Name: "admin-token", Short: "admin bearer (default: $FAAS_ADMIN_TOKEN)"},
					{Name: "timeout", Short: "HTTP timeout for the apid round-trip (default 10s)"},
				},
			},
			{
				Name:  subObsCapacity,
				Short: "Fetch GET /v1/admin/obs/capacity (admin scope + MFA required)",
				Flags: []cliFlag{
					{Name: "json", Short: "emit structured JSON"},
					{Name: "admin-token", Short: "admin bearer (default: $FAAS_ADMIN_TOKEN)"},
					{Name: "timeout", Short: "HTTP timeout for the apid round-trip (default 10s)"},
				},
			},
		},
	},
	{
		// ADR-127 PR-D — operator-side smoke harness for the
		// OTel spans writer. Posts a hand-crafted 3-span
		// ExportTraceServiceRequest to the local gatewayd-public
		// and asserts 200 + accepted_spans == 3. Used for
		// end-to-end verification at PR-D ship time — the
		// operator runs the smoke, then runs a `psql` SELECT
		// on request_telemetry.spans_summary to confirm the
		// writer landed.
		Name:    dispatchDebug,
		DocSlug: "debug",
		Short:   "Operator debugging: the OTel spans writer smoke (otel-smoke) and daemon profiles (pprof)",
		Subcommands: []cliSub{
			{
				Name:  subDebugOtel,
				Short: "POST a 3-span ExportTraceServiceRequest to gatewayd-public /v1/otel/v1/traces and assert 200 + accepted_spans==3",
				Flags: []cliFlag{
					{Name: "token", Short: "Bearer token for the OTLP POST (default: $FAAS_API_KEY)"},
					{Name: "trace-id", Short: "32-char lowercase hex trace_id for the smoke (default: all-zero)"},
					{Name: "expected-spans", Short: "expected accepted_spans value from the handler response (default 3)"},
					{Name: "timeout", Short: "HTTP timeout for the gatewayd-public round-trip (default 10s)"},
					{Name: "json", Short: "emit structured JSON to stdout (overrides human summary)"},
				},
			},
			{
				Name:  subDebugPprof,
				Short: "Capture a Go profile from a platform daemon on this host (root; ADR-20261023200002)",
				Flags: []cliFlag{
					{Name: "daemon", Short: "daemon to profile (schedd, apid, vmmd, gatewayd-internal, …)"},
					{Name: "pid", Short: "the daemon's pid, when two generations run (see --list)"},
					{Name: "profile", Short: "cpu, trace, heap, allocs, goroutine, threadcreate, block or mutex (default heap)"},
					{Name: "seconds", Short: "cpu and trace: how long to record (1..60; default 30)"},
					{Name: "debug", Short: "named profiles: 0 binary, 1 or 2 text (goroutine 2 = full dump)"},
					{Name: "gc", Short: "heap: run a GC first so the profile shows live memory"},
					{Name: "o", Short: "output file (default <daemon>-<profile>-<time>.<ext>)"},
					{Name: "list", Short: "list the daemons serving profiles on this host"},
				},
			},
		},
	},
	// Internal surface — version, completion, man.
	{
		Name:    "version",
		DocSlug: "version",
		Short:   "Print the CLI version",
	},
	{
		Name:    "completion",
		DocSlug: "completion",
		Short:   "Print a shell completion script (bash|zsh|fish|powershell)",
		Subcommands: []cliSub{
			{Name: "bash", Short: "Print the bash completion script"},
			{Name: "zsh", Short: "Print the zsh completion script"},
			{Name: "fish", Short: "Print the fish completion script"},
			{Name: "powershell", Short: "Print the powershell completion snippet"},
		},
	},
	{
		Name:        "man",
		DocSlug:     "man",
		Short:       "Print the gregalectl(1) man page (or gregalectl-<command>(1) with one arg)",
		Positionals: []string{"<command>"},
	},
}

func accountMutationCLIFlags() []cliFlag {
	return []cliFlag{
		{Name: "account-id", Short: "account id (uuid)", Req: true},
		{Name: "reason", Short: "audit reason slug [a-z0-9_]{1,64}", Req: true},
		{Name: "trace-id", Short: "OTel trace id (generated when omitted)"},
		{Name: "yes", Short: "acknowledge the account mutation", Req: true},
	}
}

// flagsMutationCLIFlags are the confirmation flags of every platform-flag
// write; withKey adds --key for writes to one flag.
func flagsMutationCLIFlags(withKey bool) []cliFlag {
	flags := []cliFlag{
		{Name: "reason", Short: "audit reason (3..500 characters)", Req: true},
		{Name: "trace-id", Short: "OTel trace id (generated when omitted)"},
		{Name: "yes", Short: "acknowledge the platform flag change", Req: true},
	}
	if withKey {
		flags = append([]cliFlag{{Name: "key", Short: "platform flag key", Req: true}}, flags...)
	}
	return flags
}

// flagsSubjectCLIFlags is who allow, deny and revoke act on: exactly one
// account or one organization (ADR 20261021080640).
func flagsSubjectCLIFlags() []cliFlag {
	return []cliFlag{
		{Name: "account-id", Short: "account UUID (or --org-id)"},
		{Name: "org-id", Short: "organization UUID: everyone acting under the company (or --account-id)"},
	}
}

func configMutationCLIFlags(set bool) []cliFlag {
	flags := []cliFlag{
		{Name: "key", Short: "runtime configuration key", Req: true},
		{Name: "reason", Short: "audit reason (3..500 characters)", Req: true},
		{Name: "trace-id", Short: "OTel trace id (generated when omitted)"},
		{Name: "yes", Short: "acknowledge the runtime configuration mutation", Req: true},
	}
	if set {
		return append(flags, cliFlag{Name: "value", Short: "new JSON scalar or plain string", Req: true})
	}
	return append(flags, cliFlag{Name: "version", Short: "historical version to restore", Req: true})
}
