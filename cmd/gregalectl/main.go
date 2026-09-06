// Command gregalectl is the operator-only companion CLI to gregale
// (issue #911 / ADR-110 PR-6.5).
//
// gregalectl ships the cluster-install surface that operators run by
// hand or wire into bootstrap/ansible:
//
//   - manifest validate|render       — split-box deployment manifest
//   - release bundle|install         — cluster-shipped release bundle
//   - host-age init|rotate|status    — operator host.age rotation
//   - pki init|status|rotate         — local-dev PKI bootstrap
//   - sign-keys init|rotate|status   — cosign sign keypair (local fs)
//   - node-key init|rotate|status    — per-node CapacityReport keypair
//   - backup init|unseal-archive-creds|unseal-rclone — backup creds
//   - secrets init|rotate|status|stamp               — post-bootstrap secrets
//   - trusted-publishers stays in `gregale` (admin API surface; see
//     PR-6.5 deviation note) — NOT shipped here.
//   - version, completion, man        — internal surface
//
// gregale (the customer-facing CLI) and gregalectl are separate
// binaries built from `cmd/gregale/` and `cmd/gregalectl/` respectively.
// No shared internal package at this PR — duplication is intentional
// (PR-7 may extract cmd/internal/cliutil/).
//
// Exit codes follow docs/faas_ux_spec.md §3.2: 0 ok, 1 user error, 2
// auth, 3 platform/infra.
package main

import (
	"fmt"
	"os"

	"github.com/onebox-faas/faas/pkg/wire"
)

// docsURL is the canonical link printed at the bottom of the usage
// string. Mirrors cmd/gregale/main.go:23 — operator topics land at
// docs.gregale.dev/cli/<topic> until PR-7 splits /cli/ from /operator/.
var docsURL = "https://" + wire.DocsHost

var usage = `gregalectl — operator companion CLI for cluster install + lifecycle.

Usage:
  gregalectl <command> [flags]

Commands:
  manifest     Validate/render a split-box deployment manifest (manifest validate|render; issue #911 / ADR-110)
  release      Materialise / install / rotate a cluster-shipped release bundle (release bundle|install|kgv)
  doctor       Read-only diagnostic for the cluster-shipped release bundle (doctor [--node NAME] [--release SHA] [--deep]; PR-4 / ADR-110)
  host-age     Operator host.age rotation (host-age init|rotate|status|prune-previous)
  pki          Operator local-dev PKI bootstrap (pki init|status|rotate)
  sign-keys    Provision the cosign sign keypair (sign-keys init|rotate|status; --sign-key / --verify-key)
  node-key     Provision the per-node CapacityReport signing keypair (node-key init|rotate|status)
  backup       Operator rclone / archive credentials (backup init|unseal-archive-creds|unseal-rclone)
  secrets      Post-bootstrap secrets init (secrets init|rotate|status|stamp; PR-X / issue #911 / ADR-110)
  artifact     Publish or verify release-pinned shared artifacts (artifact publish|verify)
  compute-nodes  Compute-node state machine (add|drain|drain-status|activate|force-drain; PR-A / multi-host scale-out)
  deploy        Provider-neutral node adoption + fleet topology tools (deploy claim|fleet-bundle|prepare-node|join-node|join-fleet|rollback-node|add-node)
  obs           Operator-side meta-obs health snapshot (obs health; Obs-Meta + Trace-IDs Mega-PR / C8)
  debug         Operator-side smoke harness for the OTel spans writer (debug otel-smoke; ADR-127 PR-D)
  github        GitHub delivery + Check Run recovery (status|retry-delivery|retry-check)
  version      Print the CLI version
  completion   Print a shell completion script (bash|zsh|fish|powershell)
  man          Print the gregalectl(1) man page (or gregalectl-<command>(1) with one arg)
  help         Print this usage block

Run 'gregalectl <command> --help' for command details.

Global flags:
  --json         Machine-readable output on every command. Equivalent
                 env: FAAS_JSON=1. Negate with --json=false.
Docs: ` + docsURL + `
`

func main() {
	// Keep the kernel-provided argument vector intact. The global --json
	// parser removes its flag from the slice it receives; release CLI
	// bootstrapping needs the original vector when it re-execs the verified
	// child so that global flags are not lost across the process boundary.
	os.Exit(run(append([]string(nil), os.Args[1:]...)))
}

func init() {
	// Mirror cmd/gregale/main.go:114 — wire.Version into the man-page
	// `.TH GREGALECTL(1) "version"` header at process boot.
	gregalectlVersion = wire.Version
}

func run(args []string) int {
	args = applyJSONFlag(args)
	if len(args) == 0 {
		fmt.Print(usage)
		return 0
	}
	if code, handled := maybeBootstrapReleaseCLI(args); handled {
		return code
	}
	switch args[0] {
	case "version", "--version", "-v":
		if len(args) > 1 && (args[1] == "--help" || args[1] == "-h") {
			PrintUsage(os.Stderr, "usage: gregalectl version", "version")
			return 0
		}
		fmt.Printf("gregalectl %s\n", wire.Version)
		return 0
	case "help", "--help", "-h":
		fmt.Print(usage)
		return 0
	case "completion":
		// Tier A8 / ADR-083. Routes to one of bash|zsh|fish|powershell
		// via cmdCompletion; the dispatcher is in completion.go.
		return cmdCompletion(args[1:])
	case "man":
		// Tier A8 / ADR-083. No arg → gregalectl(1); one arg →
		// gregalectl-<command>(1). Dispatcher is in man.go.
		return cmdMan(args[1:])
	case "manifest":
		// Issue #911 / ADR-110: operator-side manifest loader.
		// `gregalectl manifest validate --file=PATH` runs the
		// canonical validator (pkg/manifest.Validate);
		// `gregalectl manifest render --manifest-file=PATH` runs
		// pkg/renderer.Render (PR-2). The dispatcher is
		// cmdManifestDispatch in commands_manifest.go.
		return cmdManifestDispatch(args[1:])
	case "release":
		// Issue #911 / ADR-110: cluster-shipped release bundle
		// (PR-3). `gregalectl release bundle` materialises the
		// daemon-binary bundle and INSERTs into release_bundles;
		// `gregalectl release install` flips the local
		// /opt/faas/current symlink + stamps applied_at. The
		// dispatcher is cmdReleaseDispatch in commands_release.go.
		return cmdReleaseDispatch(args[1:])
	case dispatchDoctor:
		// PR-4 (issue #911 / ADR-110): read-only cluster diagnostic.
		// Walks the on-disk release tree + the release_bundles +
		// compute_nodes tables and reports drift. Exit 0 healthy,
		// exit 3 drift. NEVER writes. The dispatcher is
		// cmdDoctorDispatch in commands_doctor.go.
		return cmdDoctorDispatch(args[1:])
	case dispatchHostAge:
		// Operator-side host.age rotation (issue #316 / ADR-057).
		// Local fs only — never hits apid.
		return cmdHostAge(args[1:])
	case dispatchPKI:
		// Operator-side local-dev PKI bootstrap (ADR-052). Issues
		// /etc/faas/tls/{ca,<daemon>/} material for multi-box mTLS.
		return cmdPKI(args[1:])
	case dispatchSignKeys:
		// Operator-side cosign sign keypair. Writes
		// /etc/faas/secrets/{sign.key, sign-pub.pem}; never hits apid.
		return cmdSignKeys(args[1:])
	case dispatchNodeKey:
		// ADR-053 — operator-side provisioning for the per-node
		// CapacityReport signing keypair. Writes
		// /etc/faas/secrets/vmmd/{node.key (0400), node.pub (0444)}
		// and prints the key_id at init time.
		return cmdNodeKey(args[1:])
	case dispatchBackup:
		// Operator rclone config + log-archive credentials. cmdBackup
		// fans to init / unseal-rclone / unseal-archive-creds. Local
		// fs only — never hits apid.
		return cmdBackup(args[1:])
	case dispatchSecrets:
		// Post-bootstrap secrets init (issue #911 / ADR-110 PR-X).
		// Replaces v1 bootstrap.sh step 11d. cmdSecretsDispatch
		// fans to init / rotate / status. Local fs + optional
		// compute_nodes write — never hits apid.
		return cmdSecretsDispatch(args[1:])
	case dispatchArtifact:
		// Provider-neutral release artifact publication. The release workflow
		// publishes kernel/<firecracker-version> through the exact same OCI
		// storage backend that vmmd reads; node_join verifies it before the
		// compute node is allowed to start.
		return cmdArtifactDispatch(args[1:])
	case dispatchComputeNodes:
		// PR #929 (image rollout) + PR-A (multi-host scale-out
		// gap #1). Subcommands: add (operator POST → state.Store.
		// UpsertComputeNodeFromOperator), drain / drain-status /
		// activate / force-drain (→ state.Store.MarkComputeNodeInactive
		// / SetComputeNodeActive). Signature matches every other
		// dispatch* arm (see commands_release.go:cmdReleaseDispatch).
		return cmdComputeNodesDispatch(args[1:])
	case dispatchInstances:
		// P2a + P2b — operator recovery primitives. force-park
		// dials schedd directly via FAAS_SCHEDD_ADDR. force-cold-
		// boot opens a state.Store + dials schedd (latest-
		// deployment resolution mirrors the apid handler). Both
		// require --yes as a tripwire.
		return cmdInstancesDispatch(args[1:])
	case dispatchBuilds:
		// P2c — operator-side build-recovery primitive.
		// sweep-stuck opens a state.Store via FAAS_PG_DSN and
		// calls state.Store.SweepStuckRunningBuilds directly
		// (per user decision: NO builderd gRPC server). The
		// Store method is also called by pkg/builderd/reaper.go:48
		// — the CLI path is the operator's manual escape hatch
		// when the reaper's grace period is too long for an
		// incident.
		return cmdBuildsDispatch(args[1:])
	case dispatchDeploy:
		// Provider-neutral join-node is the production path. The legacy
		// add-node coordinator remains available for migration and local
		// repository workflows.
		return cmdDeployDispatch(args[1:])
	case dispatchObs:
		// Obs-Meta + Trace-IDs Mega-PR / C8 — operator-side
		// meta-obs health snapshot. `gregalectl obs health` dials
		// apid's GET /v1/admin/obs/health (admin scope + MFA +
		// FAAS_ADMIN_EMAILS allowlist), prints the closed-set
		// snapshot in human-readable form by default, or raw JSON
		// with --json / $FAAS_JSON=1. Mirrors the existing
		// `gregalectl admin` subcommand's HTTP-dial pattern.
		// Future subcommands (events / incidents) reserve the
		// `obs` dispatcher for follow-on PRs.
		return cmdObsDispatch(args[1:])
	case dispatchDebug:
		// ADR-127 PR-D — operator-side smoke harness for the
		// OTel spans writer. `gregalectl debug otel-smoke`
		// POSTs a hand-crafted 3-span ExportTraceServiceRequest
		// to the local gatewayd-public's /v1/otel/v1/traces
		// endpoint and asserts 200 + accepted_spans==3. Future
		// subcommands (capture | decode) reserve the `debug`
		// dispatcher for PR-C follow-ons.
		return cmdDebugDispatch(args[1:])
	case dispatchGithub:
		return cmdGithubDispatch(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "gregalectl: unknown command %q\nRun 'gregalectl help' for usage.\n", args[0])
		return 1
	}
}
