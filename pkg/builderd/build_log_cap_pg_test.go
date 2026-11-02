package builderd

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 20261102182856
// Against Postgres: a build's stored log stops at the cap with one marker,
// and builderd restarts continue the stored count instead of a fresh budget.
func TestPgEmitBuildLogCapAcrossRestarts(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	ctx := context.Background()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	st := state.NewPgStore(pool)
	account, err := st.CreateAccount(ctx, "capped@build-logs.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := st.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "capped-build-log", Type: state.AppTypeApp, RAMMB: 256, IdleTimeoutS: 60, MaxConcurrency: 5})
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(t.TempDir(), "source.tar.gz")
	if err := os.WriteFile(source, []byte("source"), 0o600); err != nil {
		t.Fatal(err)
	}
	dep, err := st.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindTarball, SourcePath: source, SourceBytes: 6, LogPath: filepath.Join(t.TempDir(), "build.log")})
	if err != nil {
		t.Fatal(err)
	}
	build, err := st.CreateBuild(ctx, dep.ID, dep.Kind, 6, dep.LogPath)
	if err != nil {
		t.Fatal(err)
	}
	line := strings.Repeat("p", 39) + "\n" // 40 bytes; cap 100
	notif := &fakeNotifier{}
	newCappedBuilderd(t, st, notif, 100).emitBuildLog(ctx, build.ID, line)
	restarted := newCappedBuilderd(t, st, notif, 100)
	restarted.emitBuildLog(ctx, build.ID, line)
	restarted.emitBuildLog(ctx, build.ID, line) // crosses the cap
	restarted.emitBuildLog(ctx, build.ID, line)
	newCappedBuilderd(t, st, notif, 100).emitBuildLog(ctx, build.ID, line)

	rows, _, err := st.ListDeploymentLogs(ctx, dep.ID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 || rows[0].Stream != state.DeploymentLogStreamSystem || rows[0].Line != buildLogTruncatedMarker(100) ||
		rows[1].Line != line || rows[2].Line != line {
		t.Fatalf("stored rows (newest first) = %#v", rows)
	}
	if len(notif.calls) != 3 {
		t.Fatalf("fanned out %d lines, want 2 lines and the marker", len(notif.calls))
	}
	usage, err := st.DeploymentLogUsage(ctx, dep.ID)
	if err != nil || !usage.Truncated || usage.Bytes != 80+int64(len(buildLogTruncatedMarker(100))) {
		t.Fatalf("usage = %+v, %v", usage, err)
	}
}
