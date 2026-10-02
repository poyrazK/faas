package state_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/region"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMemRuntimeLogArchiveRetention(t *testing.T) {
	testRuntimeLogArchiveRetention(t, state.NewMemStore())
}

func testRuntimeLogArchiveRetention(t *testing.T, s state.Store) {
	for _, id := range []string{"", "eu-retention"} {
		t.Run("region="+id, func(t *testing.T) {
			ctx := t.Context()
			app, _, instance := seedRuntimeLogArchive(t, s, id)
			catalog := s.(state.RuntimeLogArchiveCatalog)
			retention := s.(state.RuntimeLogArchiveRetentionStore)
			binding, err := catalog.CaptureRuntimeLogArchiveBinding(ctx, instance.ID, instance.NodeID)
			if err != nil {
				t.Fatal(err)
			}
			var refs []state.RuntimeLogArchive
			for _, day := range []string{"2026-06-01", "2026-06-02", "2026-10-02"} {
				ref := state.RuntimeLogArchive{RuntimeLogArchiveBinding: binding, Day: day, StorageKey: region.RuntimeLogArchiveKey(id, instance.ID, day)}
				if err := catalog.ReserveRuntimeLogArchive(ctx, ref); err != nil {
					t.Fatal(err)
				}
				refs = append(refs, ref)
			}
			fragment := strings.Repeat("b", 64)
			if err := catalog.PublishRuntimeLogArchiveFragment(ctx, instance.ID, refs[1].Day, 13, strings.Repeat("a", 64), fragment); err != nil {
				t.Fatal(err)
			}
			query := state.RuntimeLogArchiveRetentionQuery{BeforeDay: "2026-07-05", AfterDay: "0001-01-01", Limit: 1}
			page, err := retention.ListRuntimeLogArchiveRetentionCandidates(ctx, query)
			if err != nil || len(page) != 1 || page[0].Day != refs[0].Day {
				t.Fatalf("pending orphan not paged: %+v %v", page, err)
			}
			query.AfterDay, query.AfterInstance = page[0].Day, instance.ID
			page, err = retention.ListRuntimeLogArchiveRetentionCandidates(ctx, query)
			if err != nil || len(page) != 1 || page[0].Day != refs[1].Day {
				t.Fatalf("publication not paged: %+v %v", page, err)
			}
			if err := retention.DeleteCollectedRuntimeLogArchive(ctx, refs[1]); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("unretired metadata deleted: %v", err)
			}
			bad := refs[1]
			bad.ComputeRegion = "foreign"
			bad.StorageKey = region.RuntimeLogArchiveKey(bad.ComputeRegion, instance.ID, bad.Day)
			if err := retention.RetireRuntimeLogArchive(ctx, bad); !errors.Is(err, state.ErrInvalidArgument) {
				t.Fatalf("foreign owner retired: %v", err)
			}
			if err := retention.RetireRuntimeLogArchive(ctx, refs[1]); err != nil {
				t.Fatal(err)
			}
			if err := retention.RetireRuntimeLogArchive(ctx, refs[0]); err != nil {
				t.Fatal(err)
			} // Cannot lower the floor.
			for _, ref := range refs[:2] {
				if _, err := catalog.RuntimeLogArchiveByInstanceDay(ctx, instance.ID, ref.Day); !errors.Is(err, state.ErrRuntimeLogArchiveExpired) {
					t.Fatalf("retired archive readable: %v", err)
				}
				if err := catalog.ReserveRuntimeLogArchive(ctx, ref); !errors.Is(err, state.ErrRuntimeLogArchiveExpired) {
					t.Fatalf("retired day reserved: %v", err)
				}
				if err := catalog.PublishRuntimeLogArchive(ctx, instance.ID, ref.Day, 14, strings.Repeat("c", 64)); !errors.Is(err, state.ErrRuntimeLogArchiveExpired) {
					t.Fatalf("retired day published: %v", err)
				}
			}
			kept, err := catalog.ListAppRuntimeLogArchives(ctx, app.ID)
			if err != nil || len(kept) != 3 {
				t.Fatalf("retirement discarded retry metadata: %+v %v", kept, err)
			}
			if accepted, err := catalog.RuntimeLogArchiveFragmentPublished(ctx, instance.ID, refs[1].Day, fragment); err != nil || !accepted {
				t.Fatalf("receipt pruned before collection: %v %v", accepted, err)
			}
			for _, ref := range refs[:2] {
				if err := retention.DeleteCollectedRuntimeLogArchive(ctx, ref); err != nil {
					t.Fatal(err)
				}
			}
			if accepted, err := catalog.RuntimeLogArchiveFragmentPublished(ctx, instance.ID, refs[1].Day, fragment); err != nil || accepted {
				t.Fatalf("receipt survived collection: %v %v", accepted, err)
			}
			if _, err := catalog.CaptureRuntimeLogArchiveBinding(ctx, instance.ID, instance.NodeID); err != nil {
				t.Fatal(err)
			}
			if err := s.DeleteInstance(ctx, instance.ID); err != nil {
				t.Fatal(err)
			}
			if err := catalog.ReserveRuntimeLogArchive(ctx, refs[0]); !errors.Is(err, state.ErrRuntimeLogArchiveExpired) {
				t.Fatalf("late worker resurrected collected day after instance GC: %v", err)
			}
			if _, err := catalog.RuntimeLogArchiveByInstanceDay(ctx, instance.ID, refs[0].Day); !errors.Is(err, state.ErrRuntimeLogArchiveExpired) {
				t.Fatalf("legacy fallback fence lost: %v", err)
			}
			if err := catalog.PublishRuntimeLogArchive(ctx, instance.ID, refs[2].Day, 14, strings.Repeat("c", 64)); err != nil {
				t.Fatalf("newer day expired: %v", err)
			}
		})
	}
}
