// commands_instances.go — operator-side recovery primitives.
//
// P2a (force-park) + P2b (force-cold-boot) + P2d (force-restart)
// wired to schedd gRPC. All subcommands reuse the target and mTLS
// paths installed in /etc/faas/meterd.toml; single-box deployments
// without that file retain the unix:///run/faas/schedd.sock default.
// FAAS_SCHEDD_ADDR can override only the target for tests and incident
// response.
//
// The three subcommands are intentionally thin wrappers — they
// call the same gRPC RPCs that pkg/meterd already calls
// (`scheddgrpc.Client.ParkInstance`,
// `scheddgrpc.Client.ForceColdBootNextWake`,
// `scheddgrpc.Client.ForceRestartInstance`) and print a
// one-line result. The audit row is emitted by the apid handler,
// not here; the operator runs these from a host that may or may
// not have apid reachable (typical incident-response scenario:
// schedd is up, apid is being debugged). The schedd RPCs
// themselves do not require apid.
//
// Confirmation posture matches `compute-nodes force-drain`
// (commands_compute_nodes.go:245-280): all three subcommands
// require --yes as a tripwire against operator fat-fingering.
package main

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"os"

	"github.com/BurntSushi/toml"
	"github.com/onebox-faas/faas/pkg/scheddgrpc"
	"github.com/onebox-faas/faas/pkg/wire"
)

// dispatchInstances is the dispatcher key wired into
// cmd/gregalectl/main.go:switch alongside the other dispatch*
// consts.
const dispatchInstances = "instances"

// cmdInstancesDispatch fans to force-park / force-cold-boot /
// force-restart. Same (args []string) int signature every other
// dispatch* arm uses (see commands_release.go:cmdReleaseDispatch).
func cmdInstancesDispatch(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "gregalectl instances: missing subcommand; want force-park|force-cold-boot|force-restart")
		return 2
	}
	switch args[0] {
	case "force-park":
		return cmdInstancesForcePark(args[1:])
	case "force-cold-boot":
		return cmdInstancesForceColdBoot(args[1:])
	case "force-restart":
		return cmdInstancesForceRestart(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "gregalectl instances: unknown subcommand %q\n", args[0])
		return 2
	}
}

const operatorMeterdConfigPath = "/etc/faas/meterd.toml"

// operatorScheddConfig is the subset of meterd.toml gregalectl needs to
// reach schedd. Reusing the installed meterd client leaf keeps the local,
// root-only operator path on the same mTLS trust boundary as meterd without
// duplicating routing configuration in a second file.
type operatorScheddConfig struct {
	Target   string `toml:"schedd_socket"`
	CertPath string `toml:"schedd_tls_cert_path"`
	KeyPath  string `toml:"schedd_tls_key_path"`
	CAPath   string `toml:"schedd_tls_ca_path"`
}

func loadOperatorScheddConfig(path string) (operatorScheddConfig, error) {
	cfg := operatorScheddConfig{Target: "unix:///run/faas/schedd.sock"}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return operatorScheddConfig{}, fmt.Errorf("read schedd routing config %q: %w", path, err)
	}
	if _, err := toml.Decode(string(b), &cfg); err != nil {
		return operatorScheddConfig{}, fmt.Errorf("parse schedd routing config %q: %w", path, err)
	}
	if cfg.Target == "" {
		cfg.Target = "unix:///run/faas/schedd.sock"
	}
	return cfg, nil
}

func resolveOperatorScheddConnection(path string) (string, *tls.Config, error) {
	cfg, err := loadOperatorScheddConfig(path)
	if err != nil {
		return "", nil, err
	}
	target := cfg.Target
	if override := os.Getenv("FAAS_SCHEDD_ADDR"); override != "" {
		target = override
	}
	tlsCfg, err := wire.LoadClientTLSConfigWithPrefix("schedd_", cfg.CertPath, cfg.KeyPath, cfg.CAPath)
	if err != nil {
		return "", nil, fmt.Errorf("load schedd TLS: %w", err)
	}
	return target, tlsCfg, nil
}

// openScheddClientFromEnv dials schedd using the routing and mTLS material
// already installed for meterd. FAAS_SCHEDD_ADDR remains an explicit target
// override for tests and incident response; the TLS cluster still comes from
// meterd.toml so overriding a split-box target cannot silently downgrade it.
func openScheddClientFromEnv() (*scheddgrpc.Client, func(), error) {
	target, tlsCfg, err := resolveOperatorScheddConnection(operatorMeterdConfigPath)
	if err != nil {
		return nil, func() {}, fmt.Errorf("gregalectl instances: %w", err)
	}
	cli, err := scheddgrpc.DialContext(context.Background(), target, tlsCfg)
	if err != nil {
		return nil, func() {}, fmt.Errorf("gregalectl instances: dial schedd %s: %w", target, err)
	}
	return cli, func() { _ = cli.Close() }, nil
}

// cmdInstancesForcePark resolves --instance-id + --reason +
// --yes, dials schedd, calls ParkInstance. Mirrors the
// `compute-nodes force-drain` ack pattern at
// commands_compute_nodes.go:245-280. Audit row emission is the
// apid handler's job (handlers_admin_force_park.go); this CLI
// just drives the same RPC the meterd reaper uses.
//
// --trace-id (PR-#TBD / C6): optional OTel 32-char-hex
// forwarded via the gRPC x-faas-trace-id envelope so the
// schedd-side audit emit (operator.action.<verb>.outcome)
// joins the CLI invocation on a single key. Empty (the
// default) auto-generates a fresh trace_id so every CLI
// invocation carries one — operators rely on this for
// post-incident log stitching, and a CLI run without
// --trace-id would create a blind spot.
func cmdInstancesForcePark(args []string) int {
	fs := flag.NewFlagSet("force-park", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	instanceID := fs.String("instance-id", "", "instance id (uuid) to force-park")
	reason := fs.String("reason", "operator_force_park", "audit reason slug ([a-z0-9_]{1,64})")
	ack := fs.Bool("yes", false, "acknowledge that the instance will be evicted from the wake path")
	traceIDFlag := fs.String("trace-id", "", "OTel 32-char-hex trace id (auto-generated when empty)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *instanceID == "" {
		fmt.Fprintln(os.Stderr, "gregalectl instances force-park: --instance-id required")
		return 2
	}
	if !*ack {
		fmt.Fprintln(os.Stderr, "gregalectl instances force-park: --yes required (the instance will be evicted from the wake path)")
		return 2
	}
	traceID := *traceIDFlag
	if traceID == "" {
		traceID = wire.NewTraceID()
	}
	cli, closeFn, err := openScheddClientFromEnv()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer closeFn()
	if err := cli.ParkInstance(context.Background(), *instanceID, *reason, traceID); err != nil {
		fmt.Fprintln(os.Stderr, "gregalectl instances force-park:", err)
		return 1
	}
	//nolint:errcheck // final stdout write; best-effort status line
	fmt.Fprintf(os.Stdout, "force-parked %s reason=%s trace_id=%s\n", *instanceID, *reason, traceID)
	return 0
}

// cmdInstancesForceColdBoot resolves --app-slug + --reason +
// --yes, opens a state.Store via FAAS_PG_DSN, looks up the
// app + its latest deployment (mirrors the apid handler's
// latestDeploymentForApp), then dials schedd to call
// ForceColdBootNextWake. Matches the meterd precedent of
// using state.Store + scheddgrpc.Client directly so the CLI
// can run from a host that may not have apid reachable.
//
// Audit emission is intentionally NOT done here — the apid
// handler is the canonical audit site (handlers_admin_force_cold_
// boot.go:130) because it carries the admin actor identity
// (MFA + admin allowlist). The CLI's print is a stdout trace
// for the operator's incident log; the audit row is only
// emitted when the call originates from an authenticated apid
// request. This is the same trade-off the meterd reaper makes
// (cmd/meterd/main.go:184-198: it triggers ParkInstance but
// does NOT write a customer-facing audit row).
// cmdInstancesForceColdBoot resolves --app-slug + --reason +
// --yes, opens a state.Store via FAAS_PG_DSN, looks up the
// app + its latest deployment (mirrors the apid handler's
// latestDeploymentForApp), then dials schedd to call
// ForceColdBootNextWake. Matches the meterd precedent of
// resolving app → deployment → RPC, but dials schedd (not
// apid) because the snap-flip primitive is schedd-internal.
//
// --trace-id (PR-#TBD / C6): same auto-generate-on-empty
// contract as cmdInstancesForcePark; see that function's
// doc-comment for the rationale.
func cmdInstancesForceColdBoot(args []string) int {
	fs := flag.NewFlagSet("force-cold-boot", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	appSlug := fs.String("app-slug", "", "app slug whose latest deployment will be cold-booted on next wake")
	reason := fs.String("reason", "operator_force_cold_boot", "audit reason slug ([a-z0-9_]{1,64})")
	ack := fs.Bool("yes", false, "acknowledge that the customer's next wake will be a cold boot")
	traceIDFlag := fs.String("trace-id", "", "OTel 32-char-hex trace id (auto-generated when empty)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *appSlug == "" {
		fmt.Fprintln(os.Stderr, "gregalectl instances force-cold-boot: --app-slug required")
		return 2
	}
	if !*ack {
		fmt.Fprintln(os.Stderr, "gregalectl instances force-cold-boot: --yes required (the customer's next wake will be a cold boot)")
		return 2
	}

	st, closeFn, err := computeNodesStoreOpener()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer closeFn()

	ctx := context.Background()
	app, err := st.AppBySlug(ctx, *appSlug)
	if err != nil {
		fmt.Fprintln(os.Stderr, "gregalectl instances force-cold-boot:", err)
		return 1
	}
	dep, err := st.LatestDeployment(ctx, app.ID)
	if err != nil {
		fmt.Fprintln(os.Stderr, "gregalectl instances force-cold-boot:", err)
		return 1
	}

	traceID := *traceIDFlag
	if traceID == "" {
		traceID = wire.NewTraceID()
	}
	cli, scheddClose, err := openScheddClientFromEnv()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer scheddClose()
	snapIDs, err := cli.ForceColdBootNextWake(ctx, dep.ID, traceID)
	if err != nil {
		fmt.Fprintln(os.Stderr, "gregalectl instances force-cold-boot:", err)
		return 1
	}
	//nolint:errcheck // final stdout write; best-effort status line
	fmt.Fprintf(os.Stdout, "force-cold-boot app=%s deployment=%s reason=%s trace_id=%s snap_ids=%v\n",
		*appSlug, dep.ID, *reason, traceID, snapIDs)
	return 0
}

// cmdInstancesForceRestart is the P2d (PR #1105 follow-on to
// PR #1099) operator-initiated twin of cmdInstancesForcePark:
// kill the wedged live instance + flip the deployment's latest
// warm + init snapshots stale so the next customer Wake takes
// the cold-boot branch. Mirrors cmdInstancesForcePark
// byte-for-byte with two deltas:
//  1. gRPC RPC = ForceRestartInstance (vs ParkInstance) —
//     the new RPC added in Commit 3 of P2d.
//  2. stdout result includes the snap_ids returned by
//     schedd (vs force-park which has no snap walk).
//
// Like force-park / force-cold-boot, the audit row is
// intentionally NOT emitted here — the apid handler
// (handlers_admin_force_restart.go) is the canonical audit
// site because it carries the admin actor identity (MFA +
// admin allowlist). The CLI's print is a stdout trace for
// the operator's incident log; the audit row is only emitted
// when the call originates from an authenticated apid request.
// This is the same trade-off the meterd reaper makes
// (cmd/meterd/main.go:184-198: it triggers ParkInstance but
// does NOT write a customer-facing audit row).
//
// --yes ack mirrors the force-park / force-cold-boot posture
// at commands_compute_nodes.go:249. Without --yes the CLI
// exits 2 with a stderr message so an operator can't fat-
// finger the call.
//
// --trace-id (PR-#TBD / C6): same auto-generate-on-empty
// contract as cmdInstancesForcePark; see that function's
// doc-comment for the rationale.
func cmdInstancesForceRestart(args []string) int {
	fs := flag.NewFlagSet("force-restart", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	instanceID := fs.String("instance-id", "", "instance id (uuid) to force-restart (kill + cold-boot on next wake)")
	reason := fs.String("reason", "operator_force_restart", "audit reason slug ([a-z0-9_]{1,64})")
	ack := fs.Bool("yes", false, "acknowledge that the instance will be killed and the next wake will be a cold boot")
	traceIDFlag := fs.String("trace-id", "", "OTel 32-char-hex trace id (auto-generated when empty)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *instanceID == "" {
		fmt.Fprintln(os.Stderr, "gregalectl instances force-restart: --instance-id required")
		return 2
	}
	if !*ack {
		fmt.Fprintln(os.Stderr, "gregalectl instances force-restart: --yes required (the instance will be killed and the next wake will be a cold boot)")
		return 2
	}
	traceID := *traceIDFlag
	if traceID == "" {
		traceID = wire.NewTraceID()
	}
	cli, closeFn, err := openScheddClientFromEnv()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer closeFn()
	// ForceRestartInstance returns (snapIDs, err) in three of the
	// four outcomes:
	//   - happy           : (snapIDs, nil)         — len(snapIDs) >= 1
	//   - partial-success : (snapIDs, destroyErr)  — len(snapIDs) >= 1
	//   - race-loser      : (nil,    ErrInstanceNotRunning)
	//   - not-found       : (nil,    ErrNotFound)
	//   - unexpected      : (nil,    err)
	// The partial-success case is the load-bearing one for R6:
	// the deployment's warm + init snapshots ARE stale in the
	// database (the customer's next Wake WILL cold-boot), but
	// the destroy errored (vmmd wedged). Print snap IDs on the
	// error path so the operator learns the durable signal —
	// otherwise the CLI looks like a hard failure with no
	// upside.
	snapIDs, err := cli.ForceRestartInstance(context.Background(), *instanceID, *reason, traceID)
	if err != nil {
		fmt.Fprintln(os.Stderr, "gregalectl instances force-restart:", err)
		if len(snapIDs) > 0 {
			// Partial-success: snaps were flipped before the
			// destroy errored. Annotate the stderr line so the
			// operator's incident-log grep distinguishes
			// "destroy failed AND next wake is still cold-boot"
			// from "destroy failed AND nothing landed". The
			// audit row + operator_intent row already agree on
			// this shape (R4 review fix); the CLI is just
			// closing the surface so the operator's
			// `force-restart $id --yes` return-code is not the
			// only feedback channel.
			fmt.Fprintf(os.Stderr,
				"gregalectl instances force-restart: snap_ids_marked_stale=%v — next wake will be a cold boot despite the destroy error\n",
				snapIDs)
		}
		return 1
	}
	//nolint:errcheck // final stdout write; best-effort status line
	fmt.Fprintf(os.Stdout, "force-restart %s reason=%s trace_id=%s snap_ids=%v\n",
		*instanceID, *reason, traceID, snapIDs)
	return 0
}
