//go:build metal && linux

package fcvm

// adr: 20261016
// Native fixtures enforce the isolated, networkless repair guest contract.

import (
	"context"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	core "github.com/onebox-faas/faas/pkg/datarepair"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/wire"
	"golang.org/x/sys/unix"
)

type dataRepairMetalFixture struct {
	Kernel string
	Runner core.RunnerSpec
	Engine *core.Engine
	Input  core.RehearsalInput
}

func newDataRepairMetalFixture(t *testing.T, ctx context.Context) dataRepairMetalFixture {
	t.Helper()
	qualificationNativeHost(t, ctx)
	if os.Geteuid() != 0 || os.Getenv("DATABASE_URL") == "" {
		t.Fatal("native acceptance requires root and a disposable PostgreSQL fixture")
	}
	spec, configured, err := core.RunnerFromEnv(os.Getenv)
	if err != nil || !configured {
		t.Fatal("complete pinned repair runner metadata required", err)
	}
	kernel, base, layer := metalImages(t)
	if spec.BaseKey != base || spec.LayerKey != layer {
		t.Fatal("native base/layer paths differ from configured runner")
	}
	qualificationAssets(t, map[string]string{kernel: spec.KernelSHA256, base: spec.BaseSHA256, layer: spec.LayerSHA256})
	pool := pgtest.OpenDatabase(t) //nolint:contextcheck // pgtest owns setup/cleanup contexts; ctx bounds the fixture queries below.
	if _, err = pool.Exec(ctx, core.Schema()); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `CREATE TABLE products(id bigint PRIMARY KEY,category text NOT NULL);SELECT gregale_repair.enroll('products',ARRAY['category']);INSERT INTO products SELECT generate_series(1,500),'original'`); err != nil {
		t.Fatal(err)
	}
	scope := core.Scope{AccountID: uuid.NewString(), AppID: uuid.NewString(), PlatformTenantID: uuid.NewString(), Environment: "production"}
	source := uuid.NewString()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = core.Capture(ctx, tx, scope, source, "native-bad-import", "products", func(tx pgx.Tx) error { _, e := tx.Exec(ctx, `UPDATE products SET category='bad'`); return e }); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE products SET category='human' WHERE id<=40`); err != nil {
		t.Fatal(err)
	}
	checks := `SELECT count(*) FILTER(WHERE category='original')=460 AND count(*) FILTER(WHERE category='human')=40 FROM products`
	engine := &core.Engine{Pool: pool, CheckSQL: checks}
	p, err := engine.Prepare(ctx, scope, source, uuid.NewString())
	if err != nil || p.Candidates != 460 || p.Conflicts != 40 {
		t.Fatal("500/40 fixture", err)
	}
	cfg := pool.Config().ConnConfig
	sourceURL, err := url.Parse(cfg.ConnString())
	if err != nil {
		t.Fatal(err)
	}
	sourceURL.Path, sourceURL.RawPath = "/"+cfg.Database, ""
	input, err := (core.SnapshotExporter{SourceURL: sourceURL.String(), DumpBinary: os.Getenv("GREGALE_DATA_REPAIR_PG_DUMP")}).Export(ctx, p, checks)
	if err != nil {
		t.Fatal(err)
	}
	return dataRepairMetalFixture{Kernel: kernel, Runner: spec, Engine: engine, Input: input}
}

type dataRepairMetalPaths struct {
	Jail, Disk, Images string
	keep               bool
}

// Keep the journal on host-lifetime tmpfs, and anonymous writable images on
// the original layer's persistent filesystem. Never erase uncertain ownership.
func newDataRepairMetalPaths(t *testing.T, layer string) *dataRepairMetalPaths {
	t.Helper()
	disk, err := os.MkdirTemp(filepath.Dir(layer), ".gregale-repair-")
	if err != nil {
		t.Fatal(err)
	}
	jail, err := os.MkdirTemp("/tmp", "grr-")
	if err != nil {
		_ = os.RemoveAll(disk)
		t.Fatal(err)
	}
	p := &dataRepairMetalPaths{Jail: jail, Disk: disk, Images: filepath.Join(disk, "images")}
	mounted := false
	t.Cleanup(func() {
		if p.keep {
			t.Errorf("unconfirmed repair ownership retained: jail=%s disk=%s", p.Jail, p.Disk)
			return
		}
		if mounted {
			if err := unix.Unmount(jail, 0); err != nil {
				t.Error("repair journal unmount:", err)
				return
			}
		}
		if err := errors.Join(os.RemoveAll(jail), os.RemoveAll(disk)); err != nil {
			t.Error("repair fixture directory cleanup:", err)
		}
	})
	if err := os.Mkdir(p.Images, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := nativeDiskImageRootIdentity(p.Images); err != nil {
		t.Fatal("repair acceptance needs private ext4/XFS/Btrfs staging beside the layer:", err)
	}
	if err := unix.Mount("tmpfs", jail, "tmpfs", unix.MS_NOSUID|unix.MS_NODEV, "mode=0700,size=256m"); err != nil {
		t.Fatal(err)
	}
	mounted = true
	return p
}

func newDataRepairMetalManager(t *testing.T, f dataRepairMetalFixture, p *dataRepairMetalPaths, node string) (*Manager, *JailerVMM) {
	t.Helper()
	withCgroupRootAt(t, "/sys/fs/cgroup")
	v := newMetalVMM(t, 30*time.Second)
	v.chrootBase = p.Jail
	v.WithNativeImageStagingRoot(p.Images).WithNativeProcessRecovery()
	m := NewManager(wire.ExecRunner{}, v, Paths{Kernel: f.Kernel}, os.Getenv("FAAS_TEST_FC_VERSION"), nil, nil)
	m.WithRuntimeAdmissionNodeID(node).WithDataRepairRehearsals(node, f.Runner)
	t.Cleanup(func() {
		r := v.nativeRecovery
		r.mu.Lock()
		defer r.mu.Unlock()
		for _, lock := range []*os.File{r.daemonLock, r.diskLock} {
			if lock != nil {
				if err := lock.Close(); err != nil {
					t.Error("repair daemon ownership lock:", err)
				}
			}
		}
	})
	return m, v
}

func (f dataRepairMetalFixture) frame(node, incarnation string) core.RehearsalFrame {
	p := f.Input.Plan
	return core.RehearsalFrame{TaskID: uuid.NewString(), OperationID: p.ID, Scope: p.Scope, Binding: f.Runner.Digest(), NodeID: node, Incarnation: incarnation, PlanID: p.ID, PlanDigest: p.Digest, ChecksSHA256: p.ChecksSHA256, SnapshotSHA256: f.Input.SnapshotSHA256, PlanExpiresAt: p.ExpiresAt, Deadline: time.Now().UTC().Add(time.Duration(api.DataRepairTimeoutSeconds) * time.Second), Plan: api.PlanPro, Runner: f.Runner}
}
