package state_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/region"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/conformance"
)

func seedRuntimeLogArchive(t *testing.T, s state.Store, id string) (state.App, state.Deployment, state.Instance) {
	t.Helper()
	fx := conformance.Seed(t, s)
	app, dep, node := fx.App, fx.Deployment, fx.Node
	if id != "" {
		if _, err := s.UpsertComputeRegion(t.Context(), state.ComputeRegion{ID: id, DisplayName: id, AcceptingNewApps: true}); err != nil {
			t.Fatal(err)
		}
		app.ID, app.Slug, app.NodeID, app.ComputeRegion = "", "logs-"+uuid.NewString(), "", id
		var err error
		app, err = s.CreateApp(t.Context(), app)
		if err != nil {
			t.Fatal(err)
		}
		dep, err = s.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage})
		if err != nil {
			t.Fatal(err)
		}
		node.ID, node.Name, node.Region = "", "logs-"+uuid.NewString(), &id
		node, err = s.CreateComputeNode(t.Context(), node)
		if err != nil {
			t.Fatal(err)
		}
	}
	instance, err := s.CreateInstance(t.Context(), app.ID, dep.ID, string(state.StateRunning), 128, node.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	return app, dep, instance
}

func TestMemRuntimeLogArchives(t *testing.T) { testRuntimeLogArchives(t, state.NewMemStore()) }

func testRuntimeLogArchives(t *testing.T, s state.Store) {
	for _, id := range []string{"", "eu-logs"} {
		t.Run("region="+id, func(t *testing.T) {
			app, _, instance := seedRuntimeLogArchive(t, s, id)
			c := s.(state.RuntimeLogArchiveCatalog)
			if _, err := c.CaptureRuntimeLogArchiveBinding(t.Context(), instance.ID, uuid.NewString()); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("foreign node captured: %v", err)
			}
			binding, err := c.CaptureRuntimeLogArchiveBinding(t.Context(), instance.ID, instance.NodeID)
			if err != nil || binding.AppID != app.ID || binding.AccountID != app.AccountID || binding.ComputeRegion != id {
				t.Fatalf("binding=%+v err=%v", binding, err)
			}
			ref := state.RuntimeLogArchive{RuntimeLogArchiveBinding: binding, Day: "2026-10-02", StorageKey: region.RuntimeLogArchiveKey(id, instance.ID, "2026-10-02")}
			bad := ref
			bad.ComputeRegion = "other"
			bad.StorageKey = region.RuntimeLogArchiveKey("other", instance.ID, ref.Day)
			if err := c.ReserveRuntimeLogArchive(t.Context(), bad); !errors.Is(err, state.ErrInvalidArgument) {
				t.Fatalf("wrong region reserved: %v", err)
			}
			if err := c.ReserveRuntimeLogArchive(t.Context(), ref); err != nil {
				t.Fatal(err)
			}
			pending, err := c.ListAppRuntimeLogArchives(t.Context(), app.ID)
			if err != nil || len(pending) != 1 || pending[0].Published() {
				t.Fatalf("pending lost: %+v %v", pending, err)
			}
			if err := c.PublishRuntimeLogArchiveFragment(t.Context(), instance.ID, ref.Day, 13, strings.Repeat("a", 64), strings.Repeat("b", 64)); err != nil {
				t.Fatal(err)
			}
			if err := c.ReserveRuntimeLogArchive(t.Context(), ref); err != nil {
				t.Fatal(err)
			}
			if accepted, err := c.RuntimeLogArchiveFragmentPublished(t.Context(), instance.ID, ref.Day, strings.Repeat("b", 64)); err != nil || !accepted {
				t.Fatalf("fragment receipt lost: %v %v", accepted, err)
			}
			if err := s.MarkAccountDeletionPending(t.Context(), app.AccountID); err != nil {
				t.Fatal(err)
			}
			if _, err := c.CaptureRuntimeLogArchiveBinding(t.Context(), instance.ID, instance.NodeID); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("deleting account captured: %v", err)
			}
			if err := c.ReserveRuntimeLogArchive(t.Context(), ref); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("deleting account reserved: %v", err)
			}
			if err := c.PublishRuntimeLogArchive(t.Context(), instance.ID, ref.Day, 14, strings.Repeat("c", 64)); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("deleting account published: %v", err)
			}
			if err := s.RestoreAccount(t.Context(), app.AccountID); err != nil {
				t.Fatal(err)
			}
			if err := s.DeleteInstance(t.Context(), instance.ID); err != nil {
				t.Fatal(err)
			}
			got, err := c.RuntimeLogArchiveByInstanceDay(t.Context(), instance.ID, ref.Day)
			if err != nil || !got.Published() || got.StorageKey != ref.StorageKey || got.Bytes != 13 {
				t.Fatalf("history lost after retry/GC: %+v %v", got, err)
			}
			if err := s.DeleteApp(t.Context(), app.ID); err != nil {
				t.Fatal(err)
			}
			if err := c.ReserveRuntimeLogArchive(t.Context(), ref); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("deleted app published: %v", err)
			}
			if err := c.PublishRuntimeLogArchive(t.Context(), instance.ID, ref.Day, 14, strings.Repeat("b", 64)); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("deleted app committed: %v", err)
			}
			refs, err := c.ListAppRuntimeLogArchives(t.Context(), app.ID)
			if err != nil || len(refs) != 1 {
				t.Fatalf("cleanup lost ref: %+v %v", refs, err)
			}
		})
	}
}
