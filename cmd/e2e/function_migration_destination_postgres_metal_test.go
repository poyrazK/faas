//go:build metal && function_background_native

package e2e_test

// adr: 20261018030615
// Successful migrations, controlled replies and coordinator process loss.
// Native service/host restart and production activation remain separate gates.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/e2etest/functionmigration"
	"github.com/onebox-faas/faas/pkg/functionlifetime"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestFunctionMigrationDestinationPostgresMetal(t *testing.T) {
	if os.Getenv("FAAS_REQUIRE_FUNCTION_MIGRATION_NATIVE") != "1" || !functionlifetime.NativeQualificationBuild || os.Geteuid() != 0 {
		t.Fatal("use make test-function-migration-native on the prepared native fixture; skips cannot qualify")
	}
	c, err := functionmigration.Load(os.Getenv("FAAS_FUNCTION_MIGRATION_CONFIG"))
	if err != nil {
		t.Fatal(err)
	}
	if os.Getenv("FAAS_SKIP_PG_TESTS") != "" || os.Getenv("DATABASE_URL") == "" {
		t.Fatal("native migration requires the dedicated disposable catalog")
	}
	pg, err := pgxpool.ParseConfig(os.Getenv("DATABASE_URL"))
	if err != nil || pg.ConnConfig.Database != c.Catalog || pg.ConnConfig.TLSConfig == nil || pg.ConnConfig.TLSConfig.InsecureSkipVerify {
		t.Fatal("native catalog must match the fixture and verify its TLS peer")
	}
	pool, err := pgxpool.NewWithConfig(t.Context(), pg)
	if err != nil {
		t.Fatal("open native fixture catalog")
	}
	defer pool.Close()
	if err := pool.Ping(t.Context()); err != nil {
		t.Fatal("native fixture catalog is unavailable")
	}
	if os.Getenv("FAAS_FUNCTION_MIGRATION_COORDINATOR") != "" {
		runNativeCoordinatorChild(t, c, pool)
		return
	}
	report := functionmigration.Report{Version: functionmigration.ReportVersion, Scope: functionmigration.ReportScope, Config: c}
	for _, runtime := range []string{"node22", "node24"} {
		if !t.Run(runtime, func(t *testing.T) {
			for _, scenario := range functionmigration.Scenarios() {
				var run *functionmigration.Run
				if !t.Run(scenario, func(t *testing.T) { run = runNativeDestinationSQL(t, c, pool, runtime, scenario) }) || run == nil {
					t.Fatal("native case failed or skipped; no reply-boundary verdict")
				}
				report.Runs = append(report.Runs, *run)
			}
		}) {
			t.Fatal("native runtime did not execute every reply-boundary case")
		}
	}
	for _, h := range c.Hosts {
		nativeMigrationProbe(t, t.Context(), h.NodeID, "leakcheck", nil)
	}
	report.BothHostsLeakChecked = true
	if err := report.Check(); err != nil {
		t.Fatal(err)
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil || os.WriteFile(os.Getenv("FAAS_FUNCTION_MIGRATION_REPORT"), append(data, '\n'), 0o600) != nil {
		t.Fatal("write checked native migration evidence")
	}
}

func nativeMigrationProbe(t *testing.T, ctx context.Context, node, action string, owner *functionmigration.Owner) {
	t.Helper()
	if err := functionmigration.Probe(ctx, os.Getenv("FAAS_FUNCTION_MIGRATION_DRIVER"), os.Getenv("FAAS_FUNCTION_MIGRATION_CONFIG"), node, action, owner); err != nil {
		t.Fatal(err)
	}
}

func cleanupNativeMigrationApp(t *testing.T, h *e2etest.Harness, key, slug, appID string, s *state.PgStore, run *functionmigration.Run) {
	t.Helper()
	if _, status := doReq(t, h, key, http.MethodDelete, "/v1/apps/"+slug, nil); status >= 300 {
		t.Error("retire native migration app")
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), 90*time.Second)
	defer cancel()
	backgroundNativeWait(t, ctx, func() bool {
		instances, err := s.ListInstancesForApp(ctx, appID)
		if err != nil && !errors.Is(err, state.ErrNotFound) {
			return false
		}
		for _, ins := range instances {
			if state.State(ins.State).CountsForRAM() {
				return false
			}
		}
		return true
	})
	if run.Destination.PID != 0 {
		nativeMigrationProbe(t, ctx, run.DestinationNode, "retired", &run.Destination)
	}
	run.FinalAppRetired = true
}

func nativeSHA(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}
