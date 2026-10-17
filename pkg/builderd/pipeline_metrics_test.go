package builderd

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

type pipelineStore struct {
	MemStore             *state.MemStore
	claimErr, requeueErr error
	claims               int
}

func (f *pipelineStore) ClaimQueuedBuild(ctx context.Context, id string) (state.Build, error) {
	f.claims++
	if f.claimErr != nil {
		return state.Build{}, f.claimErr
	}
	return f.MemStore.ClaimQueuedBuild(ctx, id)
}
func (f *pipelineStore) ClaimNextQueuedBuild(ctx context.Context) (state.Build, error) {
	f.claims++
	if f.claimErr != nil {
		return state.Build{}, f.claimErr
	}
	return f.MemStore.ClaimNextQueuedBuild(ctx)
}
func (f *pipelineStore) RequeueBuildIfClaim(ctx context.Context, claim state.Build) error {
	if f.requeueErr != nil {
		return f.requeueErr
	}
	return f.MemStore.RequeueBuildIfClaim(ctx, claim)
}

func pipelineGauge(t *testing.T, reg *prometheus.Registry, name string) float64 {
	t.Helper()
	families, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range families {
		if f.GetName() == name {
			if len(f.Metric) != 1 || f.Metric[0].Gauge == nil || len(f.Metric[0].Label) != 0 {
				t.Fatalf("expected one unlabelled gauge: %v", f)
			}
			return f.Metric[0].Gauge.GetValue()
		}
	}
	t.Fatalf("missing gauge %s", name)
	return 0
}

func TestPipelineMetricsClaimOutcomes(t *testing.T) {
	for _, source := range []string{"poll", "notification"} {
		for _, tc := range []struct {
			name     string
			err      error
			canceled bool
			outcome  string
		}{
			{"no eligible row", state.ErrNotFound, false, "not_claimed"},
			{"database failure", errors.New("unavailable"), false, "error"},
			{"store deadline with live caller", context.DeadlineExceeded, false, "error"},
			{"parent cancellation", context.Canceled, true, "canceled"},
		} {
			t.Run(source+"/"+tc.name, func(t *testing.T) {
				f := &pipelineStore{MemStore: state.NewMemStore(), claimErr: tc.err}
				reg := prometheus.NewPedanticRegistry()
				b := New(statetest.NewMem(f), nil, nil, nil, nil, nil, Config{}, nil).WithPipelineMetrics(reg)
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				if tc.canceled {
					cancel()
				}
				var err error
				if source == "poll" {
					_, err = b.ProcessNext(ctx)
				} else {
					_, err = b.ProcessOne(ctx, "missing")
				}
				if !(source == "notification" && errors.Is(tc.err, state.ErrNotFound)) && !errors.Is(err, tc.err) {
					t.Fatalf("claim error changed: %v", err)
				}
				if f.claims != 1 || testutil.ToFloat64(b.pipeline.claims.WithLabelValues(source, tc.outcome)) != 1 {
					t.Fatal("claim call/outcome not observed once")
				}
				if pipelineGauge(t, reg, "builderd_build_slots_in_use") != 0 {
					t.Fatal("unsuccessful claim reserved a slot")
				}
			})
		}
	}
}

func TestPipelineMetricsFailedRequeueAfterSlotDenial(t *testing.T) {
	base := state.NewMemStore()
	src := filepath.Join(t.TempDir(), "source.tar.gz")
	makeTarballWithName(t, src, []string{"package.json", "index.js"})
	id, _, _ := seedDeployment(t, base, src)
	failed := errors.New("database write unavailable")
	f := &pipelineStore{MemStore: base, requeueErr: failed}
	reg := prometheus.NewPedanticRegistry()
	b := New(statetest.NewMem(f), nil, &fakeVM{}, NewCache(t.TempDir()), NewDetector(), nil, Config{}, slog.New(slog.NewTextHandler(io.Discard, nil))).WithPipelineMetrics(reg)
	b.withSlotDecider(func(ResidencyProbe, int) SlotDecision {
		return SlotDecision{Allowed: false, Reason: "private error text must not become a label"}
	})
	if _, err := b.ProcessNext(t.Context()); !errors.Is(err, ErrNoSlot) {
		t.Fatalf("legacy no-slot return changed: %v", err)
	}
	row, err := base.BuildByID(t.Context(), id)
	if err != nil || row.Status != state.BuildRunning {
		t.Fatalf("failed requeue hidden in state: %+v, %v", row, err)
	}
	if testutil.ToFloat64(b.pipeline.claims.WithLabelValues("poll", "claimed")) != 1 || testutil.ToFloat64(b.pipeline.requeues.WithLabelValues(requeueNoSlot, "error")) != 1 || testutil.ToFloat64(b.pipeline.admission.WithLabelValues("policy")) != 1 {
		t.Fatal("underlying requeue failure was hidden by ErrNoSlot")
	}
	f.requeueErr = nil
	if err := b.requeueClaim(t.Context(), row, requeueNoSlot); err != nil {
		t.Fatal(err)
	}
	if testutil.ToFloat64(b.pipeline.requeues.WithLabelValues(requeueNoSlot, "accepted")) != 1 {
		t.Fatal("requeue recovery not observed")
	}
	if err := b.requeueClaim(t.Context(), row, requeueNoSlot); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("expected fenced/no-longer-running claim: %v", err)
	}
	if testutil.ToFloat64(b.pipeline.requeues.WithLabelValues(requeueNoSlot, "not_requeued")) != 1 {
		t.Fatal("stale claim counted as database failure")
	}
	families, err := reg.Gather()
	if err != nil || len(families) != 5 {
		t.Fatalf("metric family inventory: %d %v", len(families), err)
	}
}

func TestPipelineMetricsSlotsConcurrentAndIdempotentRelease(t *testing.T) {
	reg := prometheus.NewPedanticRegistry()
	b := (&Builderd{}).WithPipelineMetrics(reg)
	_, release, ok := b.acquireSlot()
	if !ok {
		t.Fatal("guaranteed slot unavailable")
	}
	if pipelineGauge(t, reg, "builderd_build_slots_in_use") != 1 || pipelineGauge(t, reg, "builderd_build_slots_limit") != 1 {
		t.Fatal("slot gauges differ from enforced state")
	}
	if _, _, ok := b.acquireSlot(); ok {
		t.Fatal("second slot admitted")
	}
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() { release() })
		wg.Go(func() {
			if _, err := reg.Gather(); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if pipelineGauge(t, reg, "builderd_build_slots_in_use") != 0 || testutil.ToFloat64(b.pipeline.admission.WithLabelValues("granted")) != 1 || testutil.ToFloat64(b.pipeline.admission.WithLabelValues("limit")) != 1 {
		t.Fatal("slot counts or repeated release incorrect")
	}
}
