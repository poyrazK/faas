package state_test

import (
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 20261019102657 — endpoint slugs round-trip, are unique within an app
// only, and can be renamed and cleared.
func TestPgStoreManagedRealtimeEndpointSlug(t *testing.T) {
	s, ctx := pgStore(t)
	accountID, appID, _ := seedLiveDeploy(t, s, ctx, "-realtime-slug", "realtime-slug")
	in := pgManagedRealtimeEndpoint(accountID, appID)
	in.Slug = "chat"
	in.ChannelRules = []api.RealtimeChannelRule{{Pattern: "org:{org_id}:room:*", Read: true}}
	chat, err := s.CreateManagedRealtimeEndpointIfUnderQuota(ctx, in, 10, 50)
	if err != nil || chat.Slug != "chat" {
		t.Fatalf("create = %+v, %v", chat, err)
	}
	if read, err := s.ManagedRealtimeEndpointByID(ctx, chat.ID); err != nil || read.Slug != "chat" ||
		len(read.ChannelRules) != 1 || read.ChannelRules[0].Pattern != "org:{org_id}:room:*" {
		t.Fatalf("read = %+v, %v", read, err)
	}
	noRules := []api.RealtimeChannelRule{}
	if out, err := s.UpdateManagedRealtimeEndpoint(ctx, chat.ID, state.UpdateManagedRealtimeEndpointParams{ChannelRules: &noRules}); err != nil || len(out.ChannelRules) != 0 || out.Slug != "chat" {
		t.Fatalf("clear rules = %+v, %v", out, err)
	}
	if _, err := s.CreateManagedRealtimeEndpointIfUnderQuota(ctx, in, 10, 50); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("duplicate slug = %v, want ErrConflict", err)
	}
	plain, err := s.CreateManagedRealtimeEndpointIfUnderQuota(ctx, pgManagedRealtimeEndpoint(accountID, appID), 10, 50)
	if err != nil || plain.Slug != "" {
		t.Fatalf("create without slug = %+v, %v", plain, err)
	}
	taken := "chat"
	if _, err := s.UpdateManagedRealtimeEndpoint(ctx, plain.ID, state.UpdateManagedRealtimeEndpointParams{Slug: &taken}); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("rename onto a taken slug = %v, want ErrConflict", err)
	}
	cleared := ""
	if out, err := s.UpdateManagedRealtimeEndpoint(ctx, chat.ID, state.UpdateManagedRealtimeEndpointParams{Slug: &cleared}); err != nil || out.Slug != "" {
		t.Fatalf("clear = %+v, %v", out, err)
	}
	if out, err := s.UpdateManagedRealtimeEndpoint(ctx, plain.ID, state.UpdateManagedRealtimeEndpointParams{Slug: &taken}); err != nil || out.Slug != "chat" {
		t.Fatalf("reuse a cleared slug = %+v, %v", out, err)
	}
	listed, err := s.ListManagedRealtimeEndpointsForApp(ctx, appID)
	if err != nil {
		t.Fatal(err)
	}
	slugs := map[string]string{}
	for _, e := range listed {
		slugs[e.ID] = e.Slug
	}
	if slugs[plain.ID] != "chat" || slugs[chat.ID] != "" {
		t.Fatalf("listed slugs = %v", slugs)
	}
}
