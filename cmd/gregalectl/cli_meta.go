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
//   - release         (bundle | install | kgv rotate | kgv init [alias])
//   - doctor          (read-only cluster diagnostic)
//   - host-age        (init | rotate | status | prune-previous)
//   - pki             (init | status | rotate | list)
//   - sign-keys       (init | rotate | status)
//   - node-key        (init | rotate | status)
//   - backup          (init | unseal-rclone | unseal-archive-creds)
//   - secrets         (init | rotate | status | stamp)
//   - artifact        (publish | verify)
//   - compute-nodes   (add | list | show | drain | drain-status | activate | force-drain)
//   - deploy          (join-node | add-node)
//   - obs             (health)
//   - debug           (otel-smoke; ADR-127 PR-D)
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
		// PR-911 image rollout (PR #929 mega; ADR-110 + ADR-111). Operator
		// surfaces draining + activation of compute_nodes rows so the
		// deployctl upgrade-node orchestrator can reason about which box
		// is eligible for placement. The `add` verb is the operator-side
		// pre-registration path — it POSTs a row before vmmd boots on the
		// new box (multi-host scale-out gap #1; PR-A in the
		// tier-1-scaleout pair).
		Name:    dispatchComputeNodes,
		DocSlug: "compute-nodes",
		Short:   "Compute-node state machine (compute-nodes add|list|show|drain|drain-status|activate|force-drain)",
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
					{Name: "json", Short: "emit structured JSON to stdout"},
				},
			},
			{
				Name:  "list",
				Short: "List every registered compute node (default; --active-only filters; --json emits wire-shape)",
				Flags: []cliFlag{
					{Name: "active-only", Short: "filter to active=true rows only"},
					{Name: "json", Short: "emit structured JSON to stdout"},
				},
			},
			{
				Name:  "show",
				Short: "Show one compute node's full row + live_instance_count (--node <fqdn>; --json emits wire-shape)",
				Flags: []cliFlag{
					{Name: "node", Short: "fqdn / short-hostname of the node to show (required)"},
					{Name: "json", Short: "emit structured JSON to stdout"},
				},
			},
			{Name: "drain", Short: "Mark the node inactive (UPDATE compute_nodes SET active=false)"},
			{Name: "drain-status", Short: "Report whether any live instances remain on the node (exit 1 if so)"},
			{Name: "activate", Short: "Re-mark the node active (UPDATE compute_nodes SET active=true)"},
			{Name: "force-drain", Short: "Force-drain: --yes override for stuck nodes (operator-acknowledged)"},
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
					{Name: "secrets-dir", Short: "directory containing join secrets and pki/ (required)"},
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
					{Name: "storage-env", Short: "shared OCI storage environment"},
					{Name: "runtime-bases-env", Short: "release-bound digest-pinned runtime base refs"},
					{Name: "storage-device", Short: "optional dedicated fast-root block device"},
					{Name: "format-storage", Short: "explicitly format a supplied blank device as XFS"},
					{Name: "box-age-key", Short: "optional box-age identity source"},
					{Name: "rclone-envelope", Short: "encrypted rclone.conf envelope"},
					{Name: "archive-creds-envelope", Short: "encrypted archive credentials envelope"},
					{Name: "artifact-dir", Short: "standard directory containing join assets"},
					{Name: "ansible-vars-file", Short: "optional provider/overlay Ansible vars"},
					{Name: "skip-fleet-preflight", Short: "skip complete-fleet preflight"},
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
		// P2a + P2b of the operator-side observability mega-PR
		// (Commit 5b). Operator recovery primitives — `force-park`
		// dials schedd directly via FAAS_SCHEDD_ADDR, `force-cold-
		// boot` opens a state.Store via FAAS_PG_DSN to resolve
		// the latest deployment before dialing schedd. Both
		// require --yes as a tripwire (matches the force-drain
		// --yes ack pattern at compute-nodes force-drain).
		Name:    dispatchInstances,
		DocSlug: "instances",
		Short:   "Instance recovery primitives (instances force-park|force-cold-boot)",
		Subcommands: []cliSub{
			{
				Name:  "force-park",
				Short: "Force-park a wedged live instance via schedd's ParkInstance gRPC RPC",
				Flags: []cliFlag{
					{Name: "instance-id", Short: "instance id (uuid) to force-park (required)"},
					{Name: "reason", Short: "audit reason slug [a-z0-9_]{1,64} (default: operator_force_park)"},
					{Name: "yes", Short: "acknowledge that the instance will be evicted from the wake path (required)"},
				},
			},
			{
				Name:  "force-cold-boot",
				Short: "Mark the latest warm + init snapshots of an app's latest deployment stale",
				Flags: []cliFlag{
					{Name: "app-slug", Short: "app slug whose latest deployment will be cold-booted on next wake (required)"},
					{Name: "reason", Short: "audit reason slug [a-z0-9_]{1,64} (default: operator_force_cold_boot)"},
					{Name: "yes", Short: "acknowledge that the customer's next wake will be a cold boot (required)"},
				},
			},
		},
	},
	{
		// P2c of the operator-side observability mega-PR
		// (Commit 5c). Operator-side build-recovery primitive —
		// `sweep-stuck` opens a state.Store via FAAS_PG_DSN
		// and calls state.Store.SweepStuckRunningBuilds
		// directly (per user decision: NO builderd gRPC
		// server). The Store method is also called by
		// pkg/builderd/reaper.go:48 — the CLI path is the
		// operator's manual escape hatch when the reaper's
		// grace period is too long for an incident.
		Name:    dispatchBuilds,
		DocSlug: "builds",
		Short:   "Build-recovery primitives (builds sweep-stuck)",
		Subcommands: []cliSub{
			{
				Name:  "sweep-stuck",
				Short: "Flip every 'running' build row older than the threshold to 'failed/timeout'",
				Flags: []cliFlag{
					{Name: "older-than", Short: "threshold duration (clamped to [1m, 60m]; default 15m)"},
					{Name: "yes", Short: "acknowledge that rows older than the threshold will be flipped (required)"},
				},
			},
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
		Short:   "Cluster-shipped release bundle (release bundle|install|kgv|history|inspect; PR-3 / ADR-110, operator ledger)",
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
				Short: "Install a release on the local box (atomic symlink flip + applied_at first-write-wins stamp + compute_nodes UPSERT). --role is the dual-purpose flag: on first-boot it templates drop-ins + starts the role subset; on a running box with a different existing role it triggers PR-B in-place mutation (drain-gate, Mutate(stop+start), role UPSERT).",
				Flags: []cliFlag{
					{Name: "git-sha", Short: "40-char lowercase hex git SHA to install (required)", Req: true},
					{Name: "releases-root", Short: "releases root (default /opt/faas/releases)"},
					{Name: "node", Short: "compute_nodes.name to stamp (default: FAAS_NODE_NAME, then hostname; compute-only uses NAME.faas)"},
					{Name: "role", Short: "box role: control-plane|compute-only (ADR-112); empty = no role templating. Reads /etc/faas/first-boot.env's FAAS_BOX_ROLE when unset.", ClosedSet: []string{"", "control-plane", "compute-only"}},
					{Name: "defer-activation", Short: "keep a compute row drained until readiness gates pass"},
					{Name: "reason", Short: "operator reason recorded in the platform release ledger"},
				},
			},
			{
				Name:  subReleaseHistory,
				Short: "Show durable daemon deployment history from PostgreSQL",
				Flags: []cliFlag{
					{Name: "daemon", Short: "filter to one daemon"},
					{Name: "limit", Short: "maximum rows (default 50, maximum 500)"},
					{Name: "releases-root", Short: "releases root (default /opt/faas/releases)"},
				},
			},
			{
				Name:  subReleaseInspect,
				Short: "Inspect a release bundle and reconcile it with the current symlink",
				Flags: []cliFlag{
					{Name: "releases-root", Short: "releases root (default /opt/faas/releases)"},
				},
			},
			{
				// PR-B (ADR-113 day-2): operator escape hatch from the
				// fail-closed SBoM CVE-baseline gate. The KGV is the
				// "known good version" baseline the install path compares
				// against; rotate re-stamps it from the on-disk release
				// SBoM (or KGVZero with --from-zero). The KGV is
				// operator-confirmed, never auto-rotated.
				Name:  subReleaseKGV,
				Short: "Refresh sbom-baseline.json (release kgv rotate --git-sha SHA [--from-zero]); operator escape hatch from ADR-113's fail-closed SBoM gate",
				Flags: []cliFlag{
					{Name: "git-sha", Short: "40-char lowercase hex git SHA (required)", Req: true},
					{Name: "releases-root", Short: "releases root (default /opt/faas/releases)"},
					{Name: "from-zero", Short: "write KGVZero (zero CRITICAL/HIGH) without parsing the on-disk SBoM"},
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
		Short:   "Operator local-dev PKI bootstrap (pki init|status|list|rotate)",
		Subcommands: []cliSub{
			{Name: subPKIInit, Short: "Initialise the local PKI"},
			{Name: subPKIStatus, Short: "Show PKI status"},
			{Name: subPKIList, Short: "List PKI leaves + CA (--json; --daemon NAME; --box-role ROLE)"},
			{Name: subPKIRotate, Short: "Rotate the PKI"},
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
			{Name: "no-db", Short: "skip the compute_nodes.cert_fingerprint write"},
			{Name: "force", Short: "overwrite existing secret files (default false)"},
		},
	},
	{
		// Obs-Meta + Trace-IDs Mega-PR / C8 — operator-side
		// meta-obs health snapshot. Dials apid's
		// GET /v1/admin/obs/health (admin scope + MFA +
		// FAAS_ADMIN_EMAILS allowlist) and emits the closed-set
		// snapshot. `--json` / `$FAAS_JSON=1` overrides the
		// human-readable summary. Out-of-scope subcommands
		// (events / incidents) reserve room for follow-on PRs.
		Name:    dispatchObs,
		DocSlug: "obs",
		Short:   "Operator-side meta-obs health snapshot (obs health; Obs-Meta + Trace-IDs Mega-PR / C8)",
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
		Short:   "Operator-side smoke harness for the OTel spans writer (debug otel-smoke; ADR-127 PR-D)",
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
