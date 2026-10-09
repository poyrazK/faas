package gatewayconfirmation

// adr: 693

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"slices"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type fakeRepair struct {
	apps                                []string
	calls                               []string
	readError, refreshError, pruneError error
	polled                              chan struct{}
}

func (f *fakeRepair) ListRuntimeUpgradeGatewayRepairApps(ctx context.Context, _ string) ([]string, error) {
	if f.polled != nil {
		close(f.polled)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return f.apps, f.readError
}
func (f *fakeRepair) RefreshDeploymentWeights(_ context.Context, id string) error {
	f.calls = append(f.calls, id)
	return f.refreshError
}
func (f *fakeRepair) PruneExpiredRuntimeUpgradeGatewayReceipts(context.Context) error {
	return f.pruneError
}

func TestRepairRetainsFailedPageAndRecoversLostNotifications(t *testing.T) {
	sentinel := errors.New("synthetic failure")
	for _, kind := range []string{"read", "install", "publication_prune", "success"} {
		t.Run(kind, func(t *testing.T) {
			f := &fakeRepair{apps: []string{"app-a", "app-b"}}
			switch kind {
			case "read":
				f.readError = sentinel
			case "install":
				f.refreshError = sentinel
			case "publication_prune":
				f.pruneError = sentinel
			}
			next, err := Repair(t.Context(), f, f, "previous")
			if kind == "success" {
				if err != nil || next != "" || !slices.Equal(f.calls, f.apps) {
					t.Fatal(next, err, f.calls)
				}
				return
			}
			if !errors.Is(err, sentinel) || next != "previous" {
				t.Fatal("failed page skipped", next, err)
			}
			f.readError, f.refreshError, f.pruneError = nil, nil, nil
			if next, err := Repair(t.Context(), f, f, next); err != nil || next != "" {
				t.Fatal("failed page not recoverable", next, err)
			}
		})
	}
	f := &fakeRepair{apps: make([]string, api.RuntimeUpgradeGatewayRepairBatch)}
	for i := range f.apps {
		f.apps[i] = "app"
	}
	f.apps[len(f.apps)-1] = "last"
	if next, err := Repair(t.Context(), f, f, ""); err != nil || next != "last" {
		t.Fatal("bounded cursor not advanced", next, err)
	}
}

func TestRepairWorkerCancelsInFlightRead(t *testing.T) {
	f := &fakeRepair{polled: make(chan struct{})}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan struct{})
	go func() { Run(ctx, f, f, slog.New(slog.NewTextHandler(io.Discard, nil))); close(done) }()
	select {
	case <-f.polled:
	case <-time.After(3 * time.Second):
		t.Fatal("startup repair not attempted")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("worker ignored shutdown")
	}
}
