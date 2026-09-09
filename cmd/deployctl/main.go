// Command deployctl drives the systemd unit + daemons.json generator
// that powers DEPLOY-2 (issue #649). One Go source of truth —
// pkg/daemonunitspec — emits the 8 production daemon unit files into
// every deploy tree + the control-plane-only faas-cp.slice + the
// cd-controlplane workflow's daemons.json inventory.
//
// Subcommands:
//
//	generate [dirs...]   write regenerated units + daemons.json
//	check [dirs...]      regenerate to a tempdir, assert byte equality
//	                     against the committed files; exit 1 on drift
//	diff [dirs...]        like check, but prints the result to stdout
//	bundle-create <root> <release-id> <commit-sha> <target>
//	                      write and verify an immutable release manifest
//	bundle-check <root>   verify the manifest and every release file
//	migration-dry-run <release-id>
//	                      report host migration actions without mutating state
//	legacy-import <release-id> <commit-sha>
//	                      copy legacy binaries into a verified release baseline
//
// Invocation sites:
//
//	make generate        — `go run ./cmd/deployctl generate`
//	make generate-check  — `go run ./cmd/deployctl check`
//	make generate-diff   — `go run ./cmd/deployctl diff`
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/daemonunitspec"
	"github.com/onebox-faas/faas/pkg/deploycontroller"
	"github.com/onebox-faas/faas/pkg/releasebundle"
	"github.com/onebox-faas/faas/pkg/releaseinstall"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: deployctl <generate|check|diff> [dirs...]")
		os.Exit(2)
	}
	cmd := os.Args[1]
	args := os.Args[2:]
	switch cmd {
	case "generate":
		if err := runGenerate(args); err != nil {
			fmt.Fprintln(os.Stderr, "deployctl:", err)
			os.Exit(1)
		}
	case "check":
		if err := runCheck(args, true); err != nil {
			fmt.Fprintln(os.Stderr, "deployctl check:", err)
			os.Exit(1)
		}
	case "diff":
		if err := runCheck(args, false); err != nil {
			fmt.Fprintln(os.Stderr, "deployctl diff:", err)
			os.Exit(1)
		}
	case "bundle-create":
		if err := runBundleCreate(args); err != nil {
			fmt.Fprintln(os.Stderr, "deployctl bundle-create:", err)
			os.Exit(1)
		}
	case "bundle-check":
		if err := runBundleCheck(args); err != nil {
			fmt.Fprintln(os.Stderr, "deployctl bundle-check:", err)
			os.Exit(1)
		}
	case "deploy":
		if err := runDeploy(args); err != nil {
			fmt.Fprintln(os.Stderr, "deployctl deploy:", err)
			os.Exit(1)
		}
	case "migration-dry-run":
		if err := runMigrationDryRun(args); err != nil {
			fmt.Fprintln(os.Stderr, "deployctl migration-dry-run:", err)
			os.Exit(1)
		}
	case "legacy-import":
		if err := runLegacyImport(args); err != nil {
			fmt.Fprintln(os.Stderr, "deployctl legacy-import:", err)
			os.Exit(1)
		}
	case "upgrade-node":
		// ADR-111 image rollout orchestrator. See upgrade.go for the
		// full drain → wait → cloud-rollout → Probe-gate → activate
		// flow. The function is exported as runUpgradeNode to keep the
		// switch statement flat.
		if err := runUpgradeNode(args); err != nil {
			fmt.Fprintln(os.Stderr, "deployctl upgrade-node:", err)
			os.Exit(1)
		}
	default:
		fmt.Fprintln(os.Stderr, "unknown subcommand:", cmd)
		os.Exit(2)
	}
}

// target describes one place daemon unit files get emitted to.
type target struct {
	dir  string
	skip map[string]bool // daemon names to skip for this target
}

// sliceIndex is the slot of the tree that also ships faas-cp.slice
// (the control_plane_service ansible role). The slice is the
// control-plane wrapper, not a Registry member, so it is emitted by
// name rather than by daemon iteration.
const sliceIndex = 1

// defaultTargets — every tree systemd unit files live in. Each ansible
// service role ships ONLY the daemons it installs (the role's tasks
// copy `files/faas-<daemon>.service` verbatim), so the skip-set is the
// role's daemon set inverted. Index 0 is the legacy + dev-VM tree.
//
// ADR-143: pkg/daemonunitspec is the single source of truth for every
// production unit. Before this change the vmmd, gatewayd-internal,
// gatewayd-public and builderd roles carried hand-edited copies that
// had drifted from the Go spec (ordering, LoadCredential=, env), and
// the retired v1 `deploy/controlplane/systemd/` tombstone was still a
// generator target. `make generate-check` now gates all eight roles.
var defaultTargets = []target{
	{dir: "deploy/systemd", skip: legacySkips()},
	{dir: "deploy/ansible/roles/control_plane_service/files", skip: ansibleRoleSkips()},
	{dir: "deploy/ansible/roles/githubd_service/files", skip: only("githubd")},
	{dir: "deploy/ansible/roles/compute_only_service/files", skip: only("imaged")},
	{dir: "deploy/ansible/roles/vmmd_service/files", skip: only("vmmd")},
	{dir: "deploy/ansible/roles/gatewayd_internal_service/files", skip: only("gatewayd-internal")},
	{dir: "deploy/ansible/roles/gatewayd_public_service/files", skip: only("gatewayd-public")},
	{dir: "deploy/ansible/roles/builderd_service/files", skip: only("builderd")},
}

// ansibleRoleSkips: the control_plane_service role ships apid, meterd
// and schedd. githubd has its own role on the same box; every compute
// daemon lives on the compute-only roles.
func ansibleRoleSkips() map[string]bool {
	return map[string]bool{
		"githubd":           true,
		"vmmd":              true,
		"builderd":          true,
		"imaged":            true,
		"gatewayd-public":   true,
		"gatewayd-internal": true,
	}
}

// only returns the skip-set for a single-daemon role: every Registry
// daemon except `name` is skipped. Deriving it from the Registry means
// a new daemon is automatically excluded from every single-daemon role
// instead of silently appearing in all of them.
func only(name string) map[string]bool {
	skip := make(map[string]bool, len(daemonunitspec.Registry))
	for _, entry := range daemonunitspec.Registry {
		if entry.Name != name {
			skip[entry.Name] = true
		}
	}
	return skip
}

// legacySkips: deploy/systemd/ exists for legacy + dev VMs; doesn't
// ship githubd or meterd (those only exist on the control plane).
func legacySkips() map[string]bool {
	return map[string]bool{
		"githubd": true,
		"meterd":  true,
	}
}

// targetFor returns the target spec for a known source dir. Unknown
// paths get an empty skip set (every daemon emitted). The skip-set
// drives which daemons the generator writes; it's the source-dir's
// identity, not the destination's.
func targetFor(p string) target {
	cleaned := filepath.Clean(p)
	for _, t := range defaultTargets {
		if filepath.Clean(t.dir) == cleaned {
			return t
		}
	}
	return target{dir: p}
}

// targetsFor returns the target spec for each dir in `dirs`, in order.
// Used by runCheck so the per-source-dir skip-set travels to the
// regenerated tmpdir rather than getting lost when the destination
// path is `tmp/tree-N` instead of the canonical source dir.
func targetsFor(dirs []string) []target {
	out := make([]target, len(dirs))
	for i, d := range dirs {
		out[i] = targetFor(d)
	}
	return out
}

// isSliceTarget reports whether t is the tree that ships faas-cp.slice.
// Comparing resolved target structs (rather than dir path strings)
// survives the runCheck remap where `d` is `tmp/tree-N` and t.dir is
// the source-dir string.
func isSliceTarget(t target) bool {
	return filepath.Clean(t.dir) == filepath.Clean(defaultTargets[sliceIndex].dir)
}

// runGenerate writes units + daemons.json to the named target dirs.
// If no dirs are given, every default target + daemons.json.
func runGenerate(args []string) error {
	if len(args) == 0 {
		args = targetDirs()
	}
	return generateTo(targetsFor(args), args, "deploy/etc/daemons.json")
}

func targetDirs() []string {
	dirs := make([]string, len(defaultTargets))
	for i, t := range defaultTargets {
		dirs[i] = t.dir
	}
	return dirs
}

func runBundleCreate(args []string) error {
	if len(args) != 4 {
		return fmt.Errorf("usage: deployctl bundle-create <root> <release-id> <commit-sha> <target>")
	}
	manifest, err := releasebundle.Build(args[0], args[1], args[2], args[3], time.Now().UTC())
	if err != nil {
		return err
	}
	if err := releasebundle.Write(args[0], manifest); err != nil {
		return err
	}
	if err := releasebundle.Verify(args[0], manifest); err != nil {
		return err
	}
	fmt.Printf("release bundle %s verified (%d files)\n", manifest.ReleaseID, len(manifest.Files))
	return nil
}

func runBundleCheck(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: deployctl bundle-check <root>")
	}
	manifest, err := releasebundle.Read(args[0])
	if err != nil {
		return err
	}
	if err := releasebundle.Verify(args[0], manifest); err != nil {
		return err
	}
	fmt.Printf("release bundle %s verified (%d files)\n", manifest.ReleaseID, len(manifest.Files))
	return nil
}

func runMigrationDryRun(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: deployctl migration-dry-run <release-id>")
	}
	report, err := deploycontroller.DryRun(deploycontroller.Config{
		ReleasesRoot: "/opt/faas/releases",
		CurrentPath:  "/opt/faas/current",
		LockPath:     "/run/lock/faas-deploy.lock",
	}, args[0])
	if err != nil {
		return err
	}
	fmt.Printf("release: %s\ncurrent: %s\nrollback available: %t\nlegacy binaries: %t\nlegacy source: %t\n", report.ReleaseID, report.CurrentTarget, report.HasPreviousRelease, report.LegacyBinDir, report.LegacySourceDir)
	for _, check := range report.RequiredPaths {
		fmt.Printf("path %s: exists=%t (%s)\n", check.Path, check.Exists, check.Reason)
	}
	for _, path := range report.StaleScratchFiles {
		fmt.Printf("stale scratch candidate: %s\n", path)
	}
	for _, warning := range report.Warnings {
		fmt.Printf("warning: %s\n", warning)
	}
	for _, action := range report.Actions {
		fmt.Printf("action: %s\n", action)
	}
	return nil
}

func runLegacyImport(args []string) error {
	if len(args) != 2 {
		return fmt.Errorf("usage: deployctl legacy-import <release-id> <commit-sha>")
	}
	manifest, err := deploycontroller.ImportLegacyBin("/opt/faas/bin", "/opt/faas/releases", args[0], args[1], time.Now().UTC())
	if err != nil {
		return err
	}
	fmt.Printf("legacy release %s imported and verified (%d files); current pointer unchanged\n", manifest.ReleaseID, len(manifest.Files))
	return nil
}

func runDeploy(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: deployctl deploy <release-id>")
	}
	releaseID := args[0]
	releaseRoot := filepath.Join("/opt/faas/releases", releaseID)
	manifest, manifestErr := releasebundle.Read(releaseRoot)
	if manifestErr != nil {
		return manifestErr
	}
	runtime := defaultHostRuntime()
	controller, err := deploycontroller.New(deploycontroller.Config{
		ReleasesRoot: "/opt/faas/releases",
		CurrentPath:  "/opt/faas/current",
		LockPath:     "/run/lock/faas-deploy.lock",
	}, runtime)
	if err != nil {
		return err
	}
	auditPool, auditStore, auditIDs := beginDeploymentAudit(context.Background(), releaseRoot, manifest)
	if auditPool != nil {
		defer auditPool.Close()
	}
	deployErr := controller.Deploy(context.Background(), releaseID)
	finishDeploymentAudit(context.Background(), auditStore, auditIDs, deployErr)
	return deployErr
}

// beginDeploymentAudit records the attempt before the controller mutates
// systemd state. Ledger failures are intentionally best-effort: the release
// controller remains the source of truth for activation and its existing
// readiness/rollback guarantees must not be weakened by an unavailable
// audit database. Any rows already inserted are marked failed before the
// deploy proceeds.
func beginDeploymentAudit(ctx context.Context, releaseRoot string, manifest releasebundle.Manifest) (*pgxpool.Pool, releaseinstall.DeploymentStore, []string) {
	records, err := deploymentRecords(releaseRoot, manifest)
	if err != nil || len(records) == 0 {
		if err != nil {
			fmt.Fprintf(os.Stderr, "deployctl: deployment audit skipped: %v\n", err)
		}
		return nil, nil, nil
	}
	pool, err := openDeploymentPool(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "deployctl: deployment audit skipped: %v\n", err)
		return nil, nil, nil
	}
	store := releaseinstall.NewDeploymentStore(pool)
	ids := make([]string, 0, len(records))
	for _, record := range records {
		id, beginErr := store.Begin(ctx, record)
		if beginErr != nil {
			fmt.Fprintf(os.Stderr, "deployctl: deployment audit begin failed: %v\n", beginErr)
			for _, begunID := range ids {
				_ = store.Complete(ctx, begunID, releaseinstall.DeploymentFailed, map[string]any{"error": beginErr.Error(), "source": "deployctl"})
			}
			pool.Close()
			return nil, nil, nil
		}
		ids = append(ids, id)
	}
	return pool, store, ids
}

func finishDeploymentAudit(ctx context.Context, store releaseinstall.DeploymentStore, ids []string, deployErr error) {
	if store == nil || len(ids) == 0 {
		return
	}
	status := releaseinstall.DeploymentSucceeded
	notes := map[string]any{"source": "deployctl"}
	if deployErr != nil {
		status = releaseinstall.DeploymentFailed
		notes["error"] = deployErr.Error()
	}
	for _, id := range ids {
		if err := store.Complete(ctx, id, status, notes); err != nil {
			fmt.Fprintf(os.Stderr, "deployctl: deployment audit completion failed: %v\n", err)
		}
	}
}

func deploymentRecords(releaseRoot string, manifest releasebundle.Manifest) ([]releaseinstall.DeploymentRecord, error) {
	if !releaseinstall.ValidGitSHA(manifest.CommitSHA) {
		return nil, fmt.Errorf("manifest commit_sha %q is not a 40-character lowercase git SHA", manifest.CommitSHA)
	}
	byPath := make(map[string]releasebundle.File, len(manifest.Files))
	for _, file := range manifest.Files {
		byPath[file.Path] = file
	}
	actor := deploymentActor()
	sbomHash := ""
	if body, err := os.ReadFile(filepath.Join(releaseRoot, "release.sbom.json")); err == nil && len(body) > 0 {
		digest := sha256.Sum256(body)
		sbomHash = "sha256:" + hex.EncodeToString(digest[:])
	}
	records := make([]releaseinstall.DeploymentRecord, 0, len(daemonunitspec.Registry))
	for _, entry := range daemonunitspec.Registry {
		file, ok := byPath["bin/"+entry.Name]
		if !ok {
			continue
		}
		records = append(records, releaseinstall.DeploymentRecord{
			Daemon: entry.Name, Version: "sha256:" + file.SHA256,
			CommitSHA: manifest.CommitSHA, SBOMSHA256: sbomHash,
			DeployedBy: actor, DeployKind: releaseinstall.DeploymentDeploy,
			Notes: map[string]any{"source": "deployctl deploy", "release_id": manifest.ReleaseID},
		})
	}
	return records, nil
}

func openDeploymentPool(ctx context.Context) (*pgxpool.Pool, error) {
	dsn := strings.TrimSpace(os.Getenv("FAAS_PG_DSN"))
	if dsn == "" {
		dsn = strings.TrimSpace(os.Getenv("DATABASE_URL"))
	}
	if dsn == "" {
		dsn = "postgres:///faas?host=/run/postgresql&user=faas"
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse database DSN: %w", err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("open database pool: %w", err)
	}
	return pool, nil
}

func deploymentActor() string {
	for _, key := range []string{"FAAS_OPERATOR", "GITHUB_ACTOR", "SUDO_USER", "USER", "LOGNAME"} {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value
		}
	}
	return "deployctl"
}

// generateTo is the core: write unit files + slice + JSON to named
// dirs + daemonsPath. Each entry in `ts` is the resolved target
// (with its skip-set) for the corresponding dir in `dirs`. The
// faas-cp.slice is emitted into whichever dir is the slice target.
func generateTo(ts []target, dirs []string, daemonsPath string) error {
	sliceDir := ""
	for i, d := range dirs {
		t := ts[i]
		if err := os.MkdirAll(d, 0o755); err != nil {
			return fmt.Errorf("mkdir %s: %w", d, err)
		}
		for _, entry := range daemonunitspec.Registry {
			if t.skip[entry.Name] {
				continue
			}
			path := filepath.Join(d, "faas-"+entry.Name+".service")
			if err := os.WriteFile(path, entry.Unit().Render(), 0o644); err != nil {
				return fmt.Errorf("write %s: %w", path, err)
			}
		}
		if isSliceTarget(t) {
			sliceDir = d
		}
	}
	// faas-cp.slice lives outside the Registry (it's the wrapper,
	// not a member). Only the control_plane_service role ships it.
	if err := writeCPSlice(sliceDir); err != nil {
		return err
	}
	return writeDaemonsJSON(daemonsPath)
}

// writeCPSlice writes faas-cp.slice into the named dir. Empty dir is
// a no-op (no slice target in `dirs`); generator callers pass the
// actual tree path (committed or tmpdir), not the registry literal.
func writeCPSlice(sliceDir string) error {
	if sliceDir == "" {
		return nil
	}
	body := "[Unit]\n" +
		"Description=onebox-faas control plane (DO dev deployment)\n" +
		"\n" +
		"[Slice]\n" +
		"MemoryMax=3G\n"
	path := filepath.Join(sliceDir, "faas-cp.slice")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// DaemonsJSON is the shape emitted to deploy/etc/daemons.json.
type DaemonsJSON struct {
	Critical   []string `json:"critical"`
	BestEffort []string `json:"best_effort"`
}

func writeDaemonsJSON(path string) error {
	dj := DaemonsJSON{}
	for _, entry := range daemonunitspec.Registry {
		if entry.Critical {
			dj.Critical = append(dj.Critical, entry.Name)
		} else {
			dj.BestEffort = append(dj.BestEffort, entry.Name)
		}
	}
	sort.Strings(dj.Critical)
	sort.Strings(dj.BestEffort)
	body, err := json.MarshalIndent(dj, "", "  ")
	if err != nil {
		return err
	}
	body = append(body, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, body, 0o644)
}

// runCheck regenerates to a tempdir and compares against committed
// files. `quiet` true ⇒ exit 1 on drift without printing; `quiet` false
// (the `diff` subcommand) prints a focused diff message before exiting.
func runCheck(args []string, quiet bool) error {
	dirs := args
	if len(dirs) == 0 {
		dirs = targetDirs()
	}

	tmp, err := os.MkdirTemp("", "deployctl-check-")
	if err != nil {
		return err
	}
	defer func() {
		// Best-effort cleanup; on Windows the RemoveAll can race with
		// lingering read handles from filepath.Walk, but the OS
		// eventually reaps the dir. Failure here is non-fatal — the
		// tmp dir name is uniq and the OS clears /tmp on reboot.
		_ = os.RemoveAll(tmp)
	}()

	// Resolve skip-sets BEFORE remapping to tmpdirs — the source-dir
	// identity is what drives which daemons each tree ships, and the
	// tmpdir path no longer encodes it.
	ts := targetsFor(dirs)

	// Per-target tmpdir suffix: every ansible role target ends in
	// `files`, so `filepath.Base(d)` would collide and one role's
	// regeneration would clobber another's. The suffix must be
	// unique per `dir` regardless of trailing name.
	tmpDirs := make([]string, len(dirs))
	for i := range dirs {
		tmpDirs[i] = filepath.Join(tmp, fmt.Sprintf("tree-%d", i))
	}
	tmpJSON := filepath.Join(tmp, "daemons.json")
	if err := generateTo(ts, tmpDirs, tmpJSON); err != nil {
		return err
	}

	drift := 0
	for i, d := range dirs {
		if err := compareTrees(d, tmpDirs[i], quiet); err != nil {
			drift++
			if !quiet {
				fmt.Fprintln(os.Stderr, err)
			}
		}
	}
	committedJSON := "deploy/etc/daemons.json"
	if _, err := os.Stat(committedJSON); err == nil {
		if err := compareFiles(committedJSON, tmpJSON, quiet); err != nil {
			drift++
			if !quiet {
				fmt.Fprintln(os.Stderr, err)
			}
		}
	}

	if drift > 0 {
		if quiet {
			return fmt.Errorf("%d drifted paths; run 'make generate' and commit the result", drift)
		}
		return fmt.Errorf("%d drifted paths", drift)
	}
	if !quiet {
		fmt.Println("deployctl diff: no drift")
	}
	return nil
}

// compareTrees walks d (committed) and td (regenerated), comparing each
// file by name + bytes. The gate's policy:
//
//   - `~` drifted: generated file's bytes differ from committed ⇒ FAIL
//   - `+` only in regenerated: generated file missing from committed ⇒ FAIL
//   - `-` only in committed: committed file not generated ⇒ NOT a failure.
//     Legacy artefacts (README.md, pg-basebackup-*, *.toml.example,
//     faas.conf) are preserved on purpose — removing them is a separate ops
//     change, not a generator regression. Preserved artefacts do NOT trip
//     the gate.
//
// ADR-143: the v1 `deploy/controlplane/systemd/` tombstone was deleted;
// every remaining target is a live tree (legacy/dev VMs or an ansible
// role's files/), so `make generate-check` is the parity gate for
// everything that can reach a production host.
//
// Reports the names that drift; `quiet` controls print/no-print.
func compareTrees(committed, regenerated string, quiet bool) error {
	committedFiles, err := readFiles(committed)
	if err != nil {
		return fmt.Errorf("walk %s: %w", committed, err)
	}
	regeneratedFiles, err := readFiles(regenerated)
	if err != nil {
		return fmt.Errorf("walk %s: %w", regenerated, err)
	}

	var changed bool
	for name, ab := range committedFiles {
		bb, ok := regeneratedFiles[name]
		if !ok {
			// Preserved legacy artefact — not a failure.
			if !quiet {
				fmt.Printf("- %s (preserved)\n", filepath.Join(committed, name))
			}
			continue
		}
		if !bytesEqual(ab, bb) {
			changed = true
			if !quiet {
				fmt.Printf("~ %s (drifted)\n", filepath.Join(committed, name))
			}
		}
	}
	for name := range regeneratedFiles {
		if _, ok := committedFiles[name]; !ok {
			changed = true
			if !quiet {
				fmt.Printf("+ %s (only in regenerated)\n", filepath.Join(committed, name))
			}
		}
	}
	if changed {
		return fmt.Errorf("drift under %s", committed)
	}
	return nil
}

// compareFiles compares a single flat file pair.
func compareFiles(committed, regenerated string, quiet bool) error {
	a, err := os.ReadFile(committed)
	if err != nil {
		return fmt.Errorf("read %s: %w", committed, err)
	}
	b, err := os.ReadFile(regenerated)
	if err != nil {
		return fmt.Errorf("read %s: %w", regenerated, err)
	}
	if !bytesEqual(a, b) {
		if !quiet {
			fmt.Printf("~ %s (drifted)\n", committed)
		}
		return fmt.Errorf("daemons.json drifted")
	}
	return nil
}

func readFiles(root string) (map[string][]byte, error) {
	out := map[string][]byte{}
	if _, err := os.Stat(root); err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return nil, err
	}
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		out[rel] = b
		return nil
	})
	return out, err
}

func bytesEqual(a, b []byte) bool {
	return bytes.Equal(a, b)
}
