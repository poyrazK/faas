//go:build metal && linux

package fcvm

// adr: 20261016
// Native repair guests must retire before their resources can be reused.

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	core "github.com/onebox-faas/faas/pkg/datarepair"
	"github.com/onebox-faas/faas/pkg/fcvm/leakcheck"
)

// This gate uses the real native manager and the exact pinned PG16 base. It
// creates its own 500/40 source database; no customer data or fallback image.
func TestMetalDataRepairRehearsal(t *testing.T) {
	if os.Getenv("FAAS_DATA_REPAIR_NATIVE_TEST") != "1" {
		t.Skip("explicit native repair acceptance not selected")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
	defer cancel()
	fixture := newDataRepairMetalFixture(t, ctx)
	input, engine := fixture.Input, fixture.Engine
	p, scope, pool := input.Plan, input.Plan.Scope, engine.Pool
	paths := newDataRepairMetalPaths(t, fixture.Runner.LayerKey)
	node := uuid.NewString()
	m, _ := newDataRepairMetalManager(t, fixture, paths, node)
	_, incarnation, _, err := m.DataRepairRehearsalIdentity()
	if err != nil {
		t.Fatal(err)
	}
	f := fixture.frame(node, incarnation)
	frames := []core.RehearsalFrame{f}
	paths.keep = true
	t.Cleanup(func() {
		confirmed := true
		for _, f := range frames {
			clean, stop := context.WithTimeout(context.Background(), 30*time.Second)
			_, err := m.RetireDataRepairRehearsal(clean, f)
			stop()
			if err != nil {
				confirmed = false
				t.Errorf("native cleanup unconfirmed: %v", err)
			}
		}
		paths.keep = !confirmed
	})
	evidence, err := m.RunDataRepairRehearsal(ctx, f, input)
	if err != nil || !f.ValidEvidence(evidence) {
		t.Fatal("native rehearsal", err)
	}
	if m.LiveCount() != 0 || m.LeasedCount() != 0 {
		t.Fatal("native evidence preceded cleanup")
	}
	if _, err = m.RunDataRepairRehearsal(ctx, f, input); err == nil {
		t.Fatal("native task replayed")
	}
	// Cancel after the second physical VM has booted, while PG is initializing.
	f2 := f
	f2.TaskID = uuid.NewString()
	frames = append(frames, f2)
	runCtx, stopRun := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { _, e := m.RunDataRepairRehearsal(runCtx, f2, input); done <- e }()
	ready := time.NewTimer(30 * time.Second)
	for m.LiveCount() == 0 {
		select {
		case e := <-done:
			stopRun()
			ready.Stop()
			t.Fatal("cancel fixture ended before boot", e)
		case <-ready.C:
			stopRun()
			t.Fatal("cancel fixture did not boot")
		case <-time.After(time.Millisecond):
		}
	}
	ready.Stop()
	stopRun()
	if err = <-done; err == nil {
		t.Fatal("cancelled native VM succeeded")
	}
	if _, err = m.RetireDataRepairRehearsal(ctx, f2); err != nil {
		t.Fatal("cancel cleanup", err)
	}
	var bad, human int
	if err = pool.QueryRow(ctx, `SELECT count(*) FILTER(WHERE category='bad'),count(*) FILTER(WHERE category='human') FROM products`).Scan(&bad, &human); err != nil || bad != 460 || human != 40 {
		t.Fatal("rehearsal changed source", bad, human, err)
	}
	if _, err = engine.RecordRehearsal(ctx, scope, p.ID, p.Digest, evidence); err != nil {
		t.Fatal(err)
	}
	if _, err = engine.Approve(ctx, scope, p.ID, p.Digest, "native-qualification"); err != nil {
		t.Fatal(err)
	}
	if _, err = engine.Apply(ctx, scope, p.ID, p.Digest, uuid.NewString(), "native-qualification"); err != nil {
		t.Fatal(err)
	}
	var original int
	if err = pool.QueryRow(ctx, `SELECT count(*) FILTER(WHERE category='original'),count(*) FILTER(WHERE category='human') FROM products`).Scan(&original, &human); err != nil || original != 460 || human != 40 {
		t.Fatal("native-qualified repair result", original, human, err)
	}
	leakcheck.AssertZero(t)
	qualificationAssets(t, map[string]string{fixture.Kernel: fixture.Runner.KernelSHA256, fixture.Runner.BaseKey: fixture.Runner.BaseSHA256, fixture.Runner.LayerKey: fixture.Runner.LayerSHA256})
}
