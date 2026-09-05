// cmd/deployctl/upgrade.go — image-rollout orchestrator (ADR-111).
//
// `make upgrade-node IMAGE_TAG=<tag>` flow:
//   1. gregalectl compute-nodes drain --node <fqdn>
//      → UPDATE compute_nodes SET active=false WHERE name=<fqdn>
//   2. wait MigrateLiveLeaseSeconds (90s, per pkg/api/limits.go) + 5s
//      grace for live instances to land on peers
//   3. signal the cloud-specific image-rollout mechanism (hcloud /
//      amazon-ebs / bare-metal — each is its own .sh wrapper)
//   4. wait for the new VM to come up
//   5. poll every Lifecycle.ReadyzURL in pkg/daemonunitspec.Registry IN
//      ORDER; fail-closed if any dependency-aware readiness check reports
//      not-ready past readyTimeout. Transport probes remain the fallback.
//   6. UPDATE compute_nodes SET active=true on the node ONLY after
//      every probe passes
//
// The orchestrator runs the probe on the target box over SSH so a loopback
// readiness URL is evaluated on the box being upgraded, not the operator's
// workstation.
//
// Per ADR-066 / §14 M9: live-migration is out of scope. The drain-
// then-rollout path is the only supported upgrade mechanism.

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/daemonunitspec"
)

// upgradeArgs parses argv (everything after `deployctl upgrade-node`).
// Operator invocation:
//
//	deployctl upgrade-node --image-tag=gregale-compute-control-plane-...
//	                       --node=fsn-1
//	                       [--cloud=hcloud|amazon-ebs|bare-metal]
//	                       [--drain-timeout=120s]
//	                       [--ready-timeout=300s]
//	                       [--cloud-rollout=/path/to/cloud-specific.sh]
//	                       [--ssh-user=root]
//	                       [--ssh-key=/path/to/key]
//
// SSH flags are used by the per-daemon readiness gate (waitForReady) to
// ssh into the TARGET box being upgraded — without them, the gate
// would probe the operator's local /run/faas/*.sock (PR #929 review
// finding M5: "waitOneReady probes operator box").
type upgradeArgs struct {
	imageTag     string
	node         string
	cloud        string
	drainTimeout time.Duration
	readyTimeout time.Duration
	cloudRollout string
	sshUser      string
	sshKey       string
}

func parseUpgradeArgs(stdout io.Writer, args []string) (*upgradeArgs, error) {
	a := &upgradeArgs{
		drainTimeout: time.Duration(api.MigrateLiveLeaseSeconds)*time.Second + 5*time.Second,
		readyTimeout: 5 * time.Minute,
		cloud:        "hcloud",
		sshUser:      "root",
	}

	fs := flag.NewFlagSet("upgrade-node", flag.ContinueOnError)
	fs.SetOutput(stdout)
	fs.StringVar(&a.imageTag, "image-tag", "", "image tag to roll out (gregale-compute-{role}-{fc_release}-{kernel_version}-{git_sha})")
	fs.StringVar(&a.node, "node", "", "fqdn of the node being upgraded")
	fs.StringVar(&a.cloud, "cloud", a.cloud, "cloud provider: hcloud|amazon-ebs|bare-metal")
	fs.DurationVar(&a.drainTimeout, "drain-timeout", a.drainTimeout, "time to wait for live instances to land on peers (MigrateLiveLeaseSeconds + 5s grace)")
	fs.DurationVar(&a.readyTimeout, "ready-timeout", a.readyTimeout, "time to wait for every Lifecycle.ReadyzURL (or transport probe fallback) on every Registry entry to pass")
	fs.StringVar(&a.cloudRollout, "cloud-rollout", "", "path to the cloud-specific rollout shell wrapper (defaults to deploy/packer/cloud-rollout/<cloud>.sh)")
	fs.StringVar(&a.sshUser, "ssh-user", a.sshUser, "ssh user for the target box (default root)")
	fs.StringVar(&a.sshKey, "ssh-key", "", "path to the ssh private key for the target box (defaults to $HOME/.ssh/id_rsa if unset)")

	if err := fs.Parse(args); err != nil {
		return nil, err
	}

	if a.imageTag == "" {
		return nil, errors.New("--image-tag required (gregale-compute-{role}-...)")
	}
	if a.node == "" {
		return nil, errors.New("--node required (fqdn of the target box)")
	}
	if a.sshKey == "" {
		a.sshKey = filepath.Join(os.Getenv("HOME"), ".ssh", "id_rsa")
	}
	if !strings.HasPrefix(a.imageTag, "gregale-compute-") {
		return nil, fmt.Errorf("--image-tag must satisfy the ADR-111 contract; got %q", a.imageTag)
	}

	return a, nil
}

// runUpgradeNode is the entry point invoked from main.go's switch.
func runUpgradeNode(args []string) error {
	a, err := parseUpgradeArgs(io.Discard, args)
	if err != nil {
		return err
	}

	logger := slog.Default()
	ctx := context.Background()

	logger.Info("upgrade-node: starting",
		"node", a.node,
		"image_tag", a.imageTag,
		"cloud", a.cloud,
	)

	// 1. Drain — UPDATE compute_nodes SET active=false WHERE name=<fqdn>.
	// Delegates to gregalectl compute-nodes drain (PR #914), the operator-
	// facing CLI on the same wire path as `make bootstrap`.
	if err := runDrain(ctx, a); err != nil {
		return fmt.Errorf("drain %s: %w", a.node, err)
	}
	logger.Info("upgrade-node: drained", "node", a.node)

	// 2. Wait MigrateLiveLeaseSeconds + 5s grace for live instances to
	// land on peers. The schedd rebalances naturally once the box is
	// drained (the placement algorithm skips inactive rows). If anything
	// is still on the box after the wait, exit 1 with a loud warning.
	if err := waitForDrain(ctx, a); err != nil {
		return fmt.Errorf("drain wait %s: %w", a.node, err)
	}
	logger.Info("upgrade-node: drain wait complete", "node", a.node)

	// 3. Cloud-specific rollout — hand off to the per-cloud wrapper.
	if err := runCloudRollout(ctx, a); err != nil {
		return fmt.Errorf("cloud rollout %s: %w", a.cloud, err)
	}
	logger.Info("upgrade-node: cloud rollout signal sent", "node", a.node, "cloud", a.cloud)

	// 4+5. Wait for the new VM to come up + poll every Lifecycle.ReadyzURL
	// (or the legacy transport probe when no URL is registered).
	if err := waitForReady(ctx, a); err != nil {
		return fmt.Errorf("ready gate %s: %w", a.node, err)
	}
	logger.Info("upgrade-node: ready gate passed", "node", a.node)

	// 6. Flip active=true — only after every probe passes.
	if err := runActivate(ctx, a); err != nil {
		return fmt.Errorf("activate %s: %w", a.node, err)
	}
	logger.Info("upgrade-node: activated", "node", a.node)
	return nil
}

// runDrain invokes `gregalectl compute-nodes drain --node <fqdn>`.
// PR #914's CLI emits the same UPDATE that pkg/state.PgStore.
// MarkComputeNodeInactive does (pkg/state/pgstore.go:8720).
func runDrain(ctx context.Context, a *upgradeArgs) error {
	cmd := exec.CommandContext(ctx, "/opt/faas/current/bin/gregalectl",
		"compute-nodes", "drain", "--node", a.node)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	return cmd.Run()
}

// waitForDrain sleeps for MigrateLiveLeaseSeconds + 5s grace, then
// re-checks via gregalectl compute-nodes drain-status. The schedd's
// rebalance is asynchronous (every 30s per the heartbeat tick); the
// wait absorbs a full tick + a margin.
func waitForDrain(ctx context.Context, a *upgradeArgs) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(a.drainTimeout):
	}
	cmd := exec.CommandContext(ctx, "/opt/faas/current/bin/gregalectl",
		"compute-nodes", "drain-status", "--node", a.node)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		return errors.New("instances still on node — run 'gregalectl compute-nodes force-drain' to override")
	}
	return nil
}

// runCloudRollout invokes the cloud-specific wrapper. The wrapper's
// contract: it takes (node, image_tag), performs the cloud-specific
// image swap (Hetzner rebuild, AWS AMI-rotate, PXE-boot), and exits 0
// on success.
func runCloudRollout(ctx context.Context, a *upgradeArgs) error {
	rolloutScript := a.cloudRollout
	if rolloutScript == "" {
		rolloutScript = fmt.Sprintf("deploy/packer/cloud-rollout/%s.sh", a.cloud)
	}
	cmd := exec.CommandContext(ctx, "bash", rolloutScript, a.node, a.imageTag)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	return cmd.Run()
}

// waitForReady polls every Lifecycle.ReadyzURL in
// pkg/daemonunitspec.Registry IN REGISTRATION ORDER until every entry
// reports ready. The probes run against the TARGET box (a.node),
// not the operator's host — every probe is wrapped in an ssh hop.
//
// Each sshExec invokes the canonical readiness check on the remote box;
// the gate is on the box being upgraded, not the operator's box
// (PR #929 review-fix M5).
func waitForReady(ctx context.Context, a *upgradeArgs) error {
	deadline := time.Now().Add(a.readyTimeout)
	for _, entry := range daemonunitspec.Registry {
		if time.Now().After(deadline) {
			return fmt.Errorf("ready gate: deadline exceeded before %s", entry.Name)
		}
		if err := waitOneReadyOnTarget(ctx, a, entry, time.Until(deadline)); err != nil {
			return fmt.Errorf("ready gate: %s not ready: %w", entry.Name, err)
		}
	}
	return nil
}

// waitOneReadyOnTarget probes entry.Lifecycle.ReadyzURL on the TARGET box
// via ssh. The local-host probes (127.0.0.1) are remapped to the
// target's loopback; cross-host probes dial via the box's eth0.
//
// Per the registry's Lifecycle conventions:
//
//	ReadyzURL    → `curl` the dependency-aware endpoint on the target
//	ProbeUnix    → /run/faas/<daemon>.sock fallback
//	ProbeTCP     → 127.0.0.1:<port> fallback
//	ProbeSystemd → `systemctl is-active faas-<daemon>.service` fallback
func waitOneReadyOnTarget(ctx context.Context, a *upgradeArgs, entry daemonunitspec.Entry, timeout time.Duration) error {
	if timeout < 0 {
		timeout = 0
	}
	deadline := time.Now().Add(timeout)

	var probeCmd string
	if entry.Lifecycle.ReadyzURL != "" {
		// Registry-owned URLs are fixed loopback endpoints, so quoting the
		// complete URL keeps the remote shell boundary explicit without
		// allowing a future URL edit to become shell syntax.
		probeCmd = fmt.Sprintf("curl --fail --silent --show-error --max-time 2 %q >/dev/null", entry.Lifecycle.ReadyzURL)
	} else {
		switch entry.Lifecycle.Probe {
		case daemonunitspec.ProbeUnix:
			probeCmd = fmt.Sprintf("test -S %s", entry.Lifecycle.ProbeTarget)
		case daemonunitspec.ProbeTCP:
			// ProbeTarget is host:port. Re-target localhost loopback since
			// the ssh session is already inside the box.
			_, port, splitErr := net.SplitHostPort(entry.Lifecycle.ProbeTarget)
			if splitErr != nil {
				return fmt.Errorf("daemon %s: bad tcp probe %q: %w", entry.Name, entry.Lifecycle.ProbeTarget, splitErr)
			}
			probeCmd = fmt.Sprintf("bash -c 'echo > /dev/tcp/127.0.0.1/%s'", port)
		case daemonunitspec.ProbeSystemd:
			probeCmd = fmt.Sprintf("systemctl is-active faas-%s.service", entry.Name)
		default:
			return fmt.Errorf("unknown readiness probe for %s", entry.Name)
		}
	}

	for time.Now().Before(deadline) {
		err := sshProbeTarget(ctx, a, probeCmd)
		if err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	if entry.Lifecycle.ReadyzURL != "" {
		return fmt.Errorf("daemon %s /readyz not ready within %s", entry.Name, timeout)
	}
	return fmt.Errorf("daemon %s probe %s not ready within %s", entry.Name, entry.Lifecycle.Probe, timeout)
}

// sshProbeTarget invokes probeCmd on the target box via ssh. The
// agent key (a.sshKey) is the only auth path; StrictHostKeyChecking
// is permissive on first contact (the upgrade orchestrator is itself
// a fresh VM whose fingerprint isn't pinned).
func sshProbeTarget(ctx context.Context, a *upgradeArgs, probeCmd string) error {
	cmd := exec.CommandContext(ctx, "ssh",
		"-i", a.sshKey,
		"-o", "StrictHostKeyChecking=accept-new",
		"-o", "UserKnownHostsFile=/dev/null",
		"-o", "BatchMode=yes",
		fmt.Sprintf("%s@%s", a.sshUser, a.node),
		probeCmd,
	)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	return cmd.Run()
}

// runActivate flips compute_nodes.active=true via gregalectl
// compute-nodes activate — same wire path as drain.
func runActivate(ctx context.Context, a *upgradeArgs) error {
	cmd := exec.CommandContext(ctx, "/opt/faas/current/bin/gregalectl",
		"compute-nodes", "activate", "--node", a.node)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	return cmd.Run()
}
