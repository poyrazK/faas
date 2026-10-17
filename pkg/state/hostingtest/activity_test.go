package hostingtest

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/hosting/activity"
	"github.com/onebox-faas/faas/pkg/state"
)

func hostingActivityLifecycle(t *testing.T, store hostingTestStore) {
	// adr: 202610171147 — keyset reads preserve retained identities after release cleanup.
	account, site := seedDurable(t, store)
	ctx := t.Context()
	var newest api.HostingCIRun
	total := api.HostingActivityPageSize + 3
	for i := range total {
		run, err := store.CreateHostingCIRun(ctx, account.ID, site.ID, fmt.Sprintf("44444444-4444-4444-8444-%012d", i), api.HostingCIRunRequest{Manifest: manifest(), PreviewTTLSeconds: 3600, Provenance: &api.HostingReleaseProvenance{Description: "activity source"}})
		if err != nil {
			t.Fatal(err)
		}
		newest = run
		if i == total-1 {
			if _, err = store.ReadyHostingRelease(ctx, account.ID, site.ID, run.ReleaseID); err != nil {
				t.Fatal(err)
			}
			if _, err = store.CreateHostingCIPreview(ctx, account.ID, site.ID, run.ID); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if _, err = store.RetireHostingRelease(ctx, account.ID, site.ID, run.ReleaseID); err != nil {
			t.Fatal(err)
		}
		snap, err := store.HostingRouteSnapshot(ctx, site.ID)
		if err != nil {
			t.Fatal(err)
		}
		if err = store.AcknowledgeHostingRoute(ctx, site.ID, snap.Generation, time.Now().Add(-2*api.HostingRetirementGrace)); err != nil {
			t.Fatal(err)
		}
		if err = store.DeleteHostingReleaseRecord(ctx, account.ID, site.ID, run.ReleaseID); err != nil {
			t.Fatal(err)
		}
	}
	first, err := store.ListHostingActivityRecords(ctx, account.ID, site.ID, "")
	if err != nil || len(first.Records) != api.HostingActivityPageSize || first.NextCursor == "" {
		t.Fatal(first, err)
	}
	retained := first.Records[0]
	if retained.Run.ID != newest.ID || !retained.Run.PreviewCreated || retained.Release == nil || retained.Preview == nil || retained.Release.Provenance.Description != "activity source" || retained.Run.Fingerprint != "" || len(retained.Release.Manifest.Files) != 0 || retained.Preview.Hostname != "" {
		t.Fatal("projection", retained)
	}
	first.Records[0].Release.Provenance.Description = "alias mutation"
	again, err := store.ListHostingActivityRecords(ctx, account.ID, site.ID, "")
	if err != nil || again.Records[0].Release.Provenance.Description != "activity source" {
		t.Fatal("read alias", err)
	}
	late, err := store.CreateHostingCIRun(ctx, account.ID, site.ID, uuid.NewString(), api.HostingCIRunRequest{Manifest: manifest(), PreviewTTLSeconds: 3600})
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.ListHostingActivityRecords(ctx, account.ID, site.ID, first.NextCursor)
	if err != nil || len(second.Records) != 3 || second.NextCursor != "" {
		t.Fatal(second, err)
	}
	for _, row := range second.Records {
		if row.Run.ID == late.ID {
			t.Fatal("newer insertion shifted the older page")
		}
	}
	seen := map[string]bool{}
	for _, rows := range [][]state.HostingActivityRecord{first.Records, second.Records} {
		for _, row := range rows {
			if seen[row.Run.ID] {
				t.Fatal("duplicate", row.Run.ID)
			}
			seen[row.Run.ID] = true
		}
	}
	for _, row := range second.Records {
		if row.Release != nil || row.Preview != nil {
			t.Fatal("GC resources invented")
		}
	}
	// Same timestamp boundary: the UUID tie-breaker must include/exclude the anchor.
	anchor := first.Records[len(first.Records)-1].Run
	high := activity.Cursor(anchor.CreatedAt, "ffffffff-ffff-ffff-ffff-ffffffffffff")
	low := activity.Cursor(anchor.CreatedAt, "00000000-0000-0000-0000-000000000001")
	included, err := store.ListHostingActivityRecords(ctx, account.ID, site.ID, high)
	if err != nil || len(included.Records) != 4 || included.Records[0].Run.ID != anchor.ID {
		t.Fatal("upper tie", included, err)
	}
	excluded, err := store.ListHostingActivityRecords(ctx, account.ID, site.ID, low)
	if err != nil || len(excluded.Records) != 3 {
		t.Fatal("lower tie", excluded, err)
	}
	if _, err = store.ListHostingActivityRecords(ctx, uuid.NewString(), site.ID, ""); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("foreign listing", err)
	}
	if _, err = store.ListHostingActivityRecords(ctx, account.ID, site.ID, "bad"); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatal("invalid cursor", err)
	}
	// Listing neither allocates replacement resources nor changes original retry intent.
	old, err := store.GetHostingCIRun(ctx, account.ID, site.ID, second.Records[0].Run.ID)
	if err != nil || !old.PreviewExpiresAt.Equal(second.Records[0].Run.PreviewExpiresAt) {
		t.Fatal("intent changed", err)
	}
}
