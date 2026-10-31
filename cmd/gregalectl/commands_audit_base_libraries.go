// commands_audit_base_libraries.go — read-only audit of the app layers that
// boot base-minimal, before base-minimal moves to Debian 13 (ADR 20261023220532
// decision 3, rollout step 4).
//
// base-minimal copies the Debian slim image's library tree. Debian 13's slim
// image no longer ships 13 libraries that Debian 12's did, so an app layer that
// links one of them without shipping it stops starting when its node converges
// the new base. This command lists every live deployment that boots
// base-minimal, dumps each of its app and sidecar layers with debugfs (no
// mount, no root), and reports every ELF file whose DT_NEEDED names a dropped
// library that the layer does not ship itself. Libraries an app opens by name
// at run time (dlopen, ctypes) are not visible to it.

package main

import (
	"bytes"
	"context"
	"debug/elf"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/storagekey"
)

const auditBaseLibrariesSub = "base-libraries"

// debian13DroppedLibraries are the sonames in base-minimal's Debian 12 library
// tree that its Debian 13 build no longer provides. Computed 2026-10-24 from
// debian:12-slim and debian:13-slim plus the packages the build stage's
// `apt-get install ca-certificates bash libc6` adds; the Debian 12 side matched
// the published base-minimal image exactly.
var debian13DroppedLibraries = []string{
	"libapt-pkg.so.6",
	"libcom_err.so.2",
	"libe2p.so.2",
	"libext2fs.so.2",
	"libffi.so.8",
	"libgcrypt.so.20",
	"libgnutls.so.30",
	"libgpg-error.so.0",
	"libidn2.so.0",
	"libp11-kit.so.0",
	"libss.so.2",
	"libtasn1.so.6",
	"libunistring.so.2",
}

// baseMinimalLayersSQL lists the layers of deployments that boot base-minimal:
// apps without a runtime (baseRefFor("")) whose deployment did not claim a
// shared runner base (deployments.runtime_base, ADR-1500).
const baseMinimalLayersSQL = `
SELECT a.slug, d.id::text, d.status, d.rootfs_key, 'main' AS layer
  FROM deployments d
  JOIN apps a ON a.id = d.app_id
 WHERE d.deleted_at IS NULL AND a.deleted_at IS NULL
   AND d.status = ANY($1::text[])
   AND COALESCE(a.runtime, '') = '' AND a.type <> 'function'
   AND d.runtime_base = ''
   AND ($2::text = '' OR a.slug = $2::text)
UNION ALL
SELECT a.slug, d.id::text, d.status, l.storage_key, l.sidecar_name
  FROM deployment_sidecar_layers l
  JOIN deployments d ON d.id = l.deployment_id
  JOIN apps a ON a.id = d.app_id
 WHERE d.deleted_at IS NULL AND a.deleted_at IS NULL
   AND d.status = ANY($1::text[])
   AND COALESCE(a.runtime, '') = '' AND a.type <> 'function'
   AND d.runtime_base = ''
   AND ($2::text = '' OR a.slug = $2::text)
 ORDER BY 1, 2, 5`

type baseAuditTarget struct {
	App          string `json:"app"`
	DeploymentID string `json:"deployment_id"`
	Status       string `json:"status"`
	Layer        string `json:"layer"`
	LayerKey     string `json:"layer_key"`
}

type baseAuditFinding struct {
	baseAuditTarget
	File    string `json:"file"`
	Library string `json:"library"`
}

type baseAuditGap struct {
	baseAuditTarget
	Reason string `json:"reason"`
}

type baseAuditReport struct {
	Libraries     []string           `json:"libraries"`
	LayersAudited int                `json:"layers_audited"`
	ELFFiles      int                `json:"elf_files"`
	Findings      []baseAuditFinding `json:"findings"`
	Unaudited     []baseAuditGap     `json:"unaudited"`
}

// Seams for tests: the database listing, the storage backend and the debugfs
// dump all touch the host.
var (
	baseAuditListLayers = listBaseMinimalLayers
	baseAuditBackend    = func(ctx context.Context) (storage.StorageBackend, error) { return artifactBackendFromEnv(ctx) }
	baseAuditDump       = dumpExt4WithDebugfs
)

func cmdAuditBaseLibraries(args []string) int {
	fs := flag.NewFlagSet(auditBaseLibrariesSub, flag.ContinueOnError)
	fs.SetOutput(osStderr)
	dsn := fs.String("pg-dsn", "", "PostgreSQL DSN (default: $FAAS_PG_DSN or $DATABASE_URL)")
	dbEnv := fs.String("db-env", "", "systemd env file containing DATABASE_URL")
	app := fs.String("app", "", "audit only this app slug")
	includeSuperseded := fs.Bool("include-superseded", false, "also audit superseded deployments (rollback targets)")
	workDir := fs.String("work-dir", os.TempDir(), "scratch directory for layer copies and dumps (needs room for the largest layer twice)")
	debugfs := fs.String("debugfs", "debugfs", "debugfs binary (e2fsprogs)")
	libraries := fs.String("libraries", strings.Join(debian13DroppedLibraries, ","), "comma-separated sonames the new base drops")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		_, _ = fmt.Fprintln(osStderr, "gregalectl audit base-libraries: positional arguments are not accepted")
		return 2
	}
	if strings.ContainsAny(*workDir, " \t\n\"'") {
		_, _ = fmt.Fprintln(osStderr, "gregalectl audit base-libraries: --work-dir must not contain spaces or quotes (debugfs command syntax)")
		return 2
	}
	dropped := map[string]bool{}
	for _, lib := range strings.Split(*libraries, ",") {
		if lib = strings.TrimSpace(lib); lib != "" {
			dropped[lib] = true
		}
	}
	if len(dropped) == 0 {
		_, _ = fmt.Fprintln(osStderr, "gregalectl audit base-libraries: --libraries is empty")
		return 2
	}
	debugfsPath, err := exec.LookPath(*debugfs)
	if err != nil {
		_, _ = fmt.Fprintln(osStderr, "gregalectl audit base-libraries: debugfs not found (install e2fsprogs or pass --debugfs):", err)
		return 3
	}
	statuses := []string{"pending", "building", "imaging", "snapshotting", "live"}
	if *includeSuperseded {
		statuses = append(statuses, "superseded")
	}

	targets, err := baseAuditListLayers(*dsn, *dbEnv, statuses, *app)
	if err != nil {
		_, _ = fmt.Fprintln(osStderr, "gregalectl audit base-libraries:", err)
		return 3
	}
	ctx := context.Background()
	backend, err := baseAuditBackend(ctx)
	if err != nil {
		_, _ = fmt.Fprintln(osStderr, "gregalectl audit base-libraries: storage backend:", err)
		return 3
	}
	report := auditBaseLayers(ctx, backend, debugfsPath, *workDir, targets, dropped)
	if err := emitBaseAuditReport(osStdout, report, jsonOutput); err != nil {
		_, _ = fmt.Fprintln(osStderr, "gregalectl audit base-libraries:", err)
		return 3
	}
	if len(report.Findings) > 0 || len(report.Unaudited) > 0 {
		return 1
	}
	return 0
}

func listBaseMinimalLayers(dsn, dbEnv string, statuses []string, app string) ([]baseAuditTarget, error) {
	pool, err := openFleetSealPool(dsn, dbEnv)
	if err != nil {
		return nil, err
	}
	defer pool.Close()
	return queryBaseMinimalLayers(context.Background(), pool, statuses, app)
}

func queryBaseMinimalLayers(ctx context.Context, pool *pgxpool.Pool, statuses []string, app string) ([]baseAuditTarget, error) {
	rows, err := pool.Query(ctx, baseMinimalLayersSQL, statuses, app)
	if err != nil {
		return nil, fmt.Errorf("list base-minimal layers: %w", err)
	}
	defer rows.Close()
	var targets []baseAuditTarget
	for rows.Next() {
		var t baseAuditTarget
		if err := rows.Scan(&t.App, &t.DeploymentID, &t.Status, &t.LayerKey, &t.Layer); err != nil {
			return nil, fmt.Errorf("list base-minimal layers: %w", err)
		}
		if t.LayerKey == "" && t.Layer == "main" {
			t.LayerKey = storagekey.Layer(t.DeploymentID)
		}
		targets = append(targets, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list base-minimal layers: %w", err)
	}
	return targets, nil
}

// auditBaseLayers audits each layer in turn. A layer that cannot be fetched,
// dumped or walked is reported as unaudited, never as clean.
func auditBaseLayers(ctx context.Context, backend storage.StorageBackend, debugfs, workDir string, targets []baseAuditTarget, dropped map[string]bool) baseAuditReport {
	report := baseAuditReport{Findings: []baseAuditFinding{}, Unaudited: []baseAuditGap{}}
	for lib := range dropped {
		report.Libraries = append(report.Libraries, lib)
	}
	sort.Strings(report.Libraries)
	for _, t := range targets {
		elfFiles, needs, err := auditOneLayer(ctx, backend, debugfs, workDir, t.LayerKey, dropped)
		if err != nil {
			report.Unaudited = append(report.Unaudited, baseAuditGap{baseAuditTarget: t, Reason: err.Error()})
			continue
		}
		report.LayersAudited++
		report.ELFFiles += elfFiles
		for _, n := range needs {
			report.Findings = append(report.Findings, baseAuditFinding{baseAuditTarget: t, File: n.file, Library: n.library})
		}
	}
	return report
}

type layerNeed struct{ file, library string }

func auditOneLayer(ctx context.Context, backend storage.StorageBackend, debugfs, workDir, key string, dropped map[string]bool) (int, []layerNeed, error) {
	scratch, err := os.MkdirTemp(workDir, "base-audit-")
	if err != nil {
		return 0, nil, fmt.Errorf("scratch dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(scratch) }()
	image, err := localLayerImage(ctx, backend, key, scratch)
	if err != nil {
		return 0, nil, err
	}
	tree := filepath.Join(scratch, "tree")
	if err := os.Mkdir(tree, 0o700); err != nil {
		return 0, nil, fmt.Errorf("dump dir: %w", err)
	}
	if err := baseAuditDump(ctx, debugfs, image, tree); err != nil {
		return 0, nil, err
	}
	return scanLayerTree(tree, dropped)
}

// localLayerImage returns a readable path for the layer: the backend's own
// file when it has one, otherwise a private copy under scratch.
func localLayerImage(ctx context.Context, backend storage.StorageBackend, key, scratch string) (string, error) {
	if resolver, ok := backend.(storage.LocalPathResolver); ok {
		if path, ok, err := resolver.LocalPath(key); err == nil && ok {
			return path, nil
		}
	}
	body, err := backend.Get(ctx, key)
	if err != nil {
		return "", fmt.Errorf("fetch layer %s: %w", key, err)
	}
	defer func() { _ = body.Close() }()
	path := filepath.Join(scratch, "layer.ext4")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return "", fmt.Errorf("copy layer %s: %w", key, err)
	}
	if _, err := io.Copy(f, body); err != nil {
		_ = f.Close()
		return "", fmt.Errorf("copy layer %s: %w", key, err)
	}
	if err := f.Close(); err != nil {
		return "", fmt.Errorf("copy layer %s: %w", key, err)
	}
	return path, nil
}

// dumpExt4WithDebugfs copies the image's tree into dir without mounting it.
// debugfs reports most errors on stderr and still exits 0, and it cannot
// change ownership when run unprivileged; an empty dir is the failure signal
// (a valid ext4 always has lost+found).
func dumpExt4WithDebugfs(ctx context.Context, debugfs, image, dir string) error {
	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, debugfs, "-R", "rdump / "+dir, image)
	cmd.Stdout = io.Discard
	cmd.Stderr = &stderr
	runErr := cmd.Run()
	entries, err := os.ReadDir(dir)
	if err == nil && len(entries) > 0 && runErr == nil {
		return nil
	}
	cause := errors.Join(runErr, err)
	if cause == nil {
		cause = errors.New("no files written")
	}
	msg := strings.TrimSpace(stderr.String())
	if len(msg) > 300 {
		msg = msg[len(msg)-300:]
	}
	return fmt.Errorf("debugfs could not dump the layer: %w: %s", cause, msg)
}

// scanLayerTree returns the number of ELF files in the dumped tree and every
// (file, library) pair where an ELF file needs a dropped library that the
// layer does not ship under any path. It never follows symlinks.
func scanLayerTree(root string, dropped map[string]bool) (int, []layerNeed, error) {
	shipped := map[string]bool{}
	var candidates []layerNeed
	elfFiles := 0
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		shipped[d.Name()] = true
		if !d.Type().IsRegular() {
			return nil
		}
		needed, isELF := elfNeededLibraries(path)
		if !isELF {
			return nil
		}
		elfFiles++
		rel, _ := filepath.Rel(root, path)
		for _, lib := range needed {
			if dropped[lib] {
				candidates = append(candidates, layerNeed{file: "/" + filepath.ToSlash(rel), library: lib})
			}
		}
		return nil
	})
	if err != nil {
		return 0, nil, fmt.Errorf("walk layer: %w", err)
	}
	var needs []layerNeed
	for _, c := range candidates {
		if !shipped[c.library] {
			needs = append(needs, c)
		}
	}
	return elfFiles, needs, nil
}

// elfNeededLibraries returns the DT_NEEDED entries of an ELF file. Files that
// are not ELF, or that debug/elf cannot parse, report isELF=false. The path
// comes from a customer layer, so it is opened without following a symlink
// and read only if it is a regular file.
func elfNeededLibraries(path string) (needed []string, isELF bool) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, false
	}
	defer func() { _ = f.Close() }()
	if info, err := f.Stat(); err != nil || !info.Mode().IsRegular() {
		return nil, false
	}
	magic := make([]byte, 4)
	if _, err := io.ReadFull(f, magic); err != nil || !bytes.Equal(magic, []byte(elf.ELFMAG)) {
		return nil, false
	}
	ef, err := elf.NewFile(f)
	if err != nil {
		return nil, false
	}
	defer func() { _ = ef.Close() }()
	libs, err := ef.ImportedLibraries()
	if err != nil {
		return nil, true
	}
	return libs, true
}

func emitBaseAuditReport(w io.Writer, r baseAuditReport, asJSON bool) error {
	if asJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(r)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "base-minimal library audit: %d layers audited, %d ELF files, %d dropped libraries checked\n",
		r.LayersAudited, r.ELFFiles, len(r.Libraries))
	if len(r.Findings) == 0 && len(r.Unaudited) == 0 {
		b.WriteString("OK: no audited layer needs a library the new base drops\n")
	}
	if len(r.Findings) > 0 {
		fmt.Fprintf(&b, "FINDINGS (%d): rebuild these apps or tell their owners before the base moves\n", len(r.Findings))
		for _, f := range r.Findings {
			fmt.Fprintf(&b, "  app=%s deployment=%s status=%s layer=%s file=%s needs %s\n",
				f.App, f.DeploymentID, f.Status, f.Layer, f.File, f.Library)
		}
	}
	if len(r.Unaudited) > 0 {
		fmt.Fprintf(&b, "UNAUDITED (%d): these layers were not checked\n", len(r.Unaudited))
		for _, g := range r.Unaudited {
			fmt.Fprintf(&b, "  app=%s deployment=%s layer=%s key=%s: %s\n", g.App, g.DeploymentID, g.Layer, g.LayerKey, g.Reason)
		}
	}
	_, err := io.WriteString(w, b.String())
	return err
}
