package hosting

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestHostingPublicationWaitOutcomes(t *testing.T) {
	// adr: 202610161920 — success means an exact durable receipt, not edge health.
	site := api.HostingSite{ID: "site", OriginID: "origin", Status: "active", Revision: 4, ActiveReleaseID: "release", DurableRouting: true}
	at := time.Now()
	good := api.HostingSiteStatus{Site: site, Publication: api.HostingPublicationStatus{State: "published", Generation: 5, PublishedGeneration: 5, PublishedAt: &at}}
	for _, tc := range []struct {
		name, expected string
		mutate         func(*api.HostingSiteStatus)
	}{
		{"published", "published", func(*api.HostingSiteStatus) {}},
		{"superseded", "superseded", func(s *api.HostingSiteStatus) { s.Site.Revision++ }},
		{"wrong release", "superseded", func(s *api.HostingSiteStatus) { s.Site.ActiveReleaseID = "other" }},
		{"foreign origin", "unavailable", func(s *api.HostingSiteStatus) { s.Site.OriginID = "other" }},
		{"missing receipt", "unavailable", func(s *api.HostingSiteStatus) { s.Publication.PublishedAt = nil }},
		{"older receipt", "unavailable", func(s *api.HostingSiteStatus) { s.Publication.PublishedGeneration-- }},
		{"untracked", "untracked", func(s *api.HostingSiteStatus) { s.Publication.State = "untracked" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			status := good
			tc.mutate(&status)
			result, err := WaitPublication(t.Context(), func(context.Context, string) (api.HostingSiteStatus, error) { return status, nil }, site)
			if result.WaitState != tc.expected || (err == nil) != (tc.expected == "published") {
				t.Fatalf("%+v %v", result, err)
			}
		})
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	result, err := WaitPublication(ctx, func(ctx context.Context, _ string) (api.HostingSiteStatus, error) {
		<-ctx.Done()
		return api.HostingSiteStatus{}, ctx.Err()
	}, site)
	if err == nil || result.WaitState != "timeout" || result.Site.ActiveReleaseID != "release" {
		t.Fatalf("timeout lost accepted intent: %+v %v", result, err)
	}
	result, err = WaitPublication(t.Context(), func(context.Context, string) (api.HostingSiteStatus, error) {
		return api.HostingSiteStatus{}, errors.New("outage")
	}, site)
	if err == nil || result.WaitState != "unavailable" {
		t.Fatalf("outage %+v %v", result, err)
	}
	site.DurableRouting = false
	if result, err = WaitPublication(t.Context(), nil, site); err == nil || result.WaitState != "untracked" {
		t.Fatal("invented legacy receipt")
	}
}

func TestHostingPublicationWaitPendingThenPublished(t *testing.T) {
	site := api.HostingSite{ID: "site", OriginID: "origin", Status: "active", Revision: 4, ActiveReleaseID: "release", DurableRouting: true}
	at := time.Now()
	calls := 0
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	result, err := WaitPublication(ctx, func(context.Context, string) (api.HostingSiteStatus, error) {
		calls++
		p := api.HostingPublicationStatus{State: "pending", Generation: 5, PublishedGeneration: 4}
		if calls == 2 {
			p.State = "published"
			p.PublishedGeneration = 5
			p.PublishedAt = &at
		}
		return api.HostingSiteStatus{Site: site, Publication: p}, nil
	}, site)
	if err != nil || result.WaitState != "published" || calls != 2 {
		t.Fatalf("%+v calls=%d %v", result, calls, err)
	}
}
