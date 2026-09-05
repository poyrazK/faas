package builderd

import (
	"context"
	"errors"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

type completionNotifier func(context.Context, string, string) error

func (n completionNotifier) Notify(ctx context.Context, ch, payload string) error {
	return n(ctx, ch, payload)
}
func TestCompletionPublicationOrderAndStaleWorker(t *testing.T) {
	for _, scenario := range []string{"lost-notify", "reaped"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := context.Background()
			s := state.NewMemStore()
			src := filepath.Join(t.TempDir(), "source.tar.gz")
			makeTarballWithName(t, src, []string{"package.json"})
			id, depID, _ := seedDeployment(t, s, src)
			layer := filepath.Join(t.TempDir(), "image.tar")
			if err := os.WriteFile(layer, []byte("image bytes"), 0600); err != nil {
				t.Fatal(err)
			}
			vm := &fakeVM{out: BuildOutcome{OCIImage: layer}}
			if scenario == "reaped" {
				vm.waitHook = func() {
					if err := s.UpdateBuildStatus(ctx, id, state.BuildFailed, state.FailureTimeout, false, true); err != nil {
						t.Fatal(err)
					}
				}
			}
			bootCalls := 0
			notify := completionNotifier(func(ctx context.Context, ch, _ string) error {
				if ch != db.NotifySnapshotBoot {
					return nil
				}
				bootCalls++
				got, err := s.BuildByID(ctx, id)
				if err != nil || got.Status != state.BuildSucceeded {
					t.Fatalf("notify preceded success: %+v %v", got, err)
				}
				if _, err := s.BuildProvenanceByBuildID(ctx, id); err != nil {
					t.Fatalf("notify preceded provenance: %v", err)
				}
				return errors.New("notification connection lost")
			})
			b := New(s, notify, vm, NewCache(t.TempDir()), NewDetector(), nil, Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
			result, err := b.ProcessOne(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			dep, _ := s.DeploymentByID(ctx, depID)
			if scenario == "reaped" {
				if bootCalls != 0 || dep.RootfsPath != "" || result.BuildID != "" {
					t.Fatalf("stale publication: %+v %+v", result, dep)
				}
				return
			}
			if bootCalls != 1 {
				t.Fatalf("boot calls %d", bootCalls)
			}
			work, err := s.ListBuildsAwaitingImage(ctx, "", 16)
			if err != nil || len(work) != 1 || work[0].DeploymentID != depID {
				t.Fatalf("lost notify not recoverable: %+v %v", work, err)
			}
		})
	}
}
