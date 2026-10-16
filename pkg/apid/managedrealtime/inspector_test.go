package managedrealtime

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestLoadInspectorScopesEndpointsToOwner(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "owner@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	owned, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "owned"})
	if err != nil {
		t.Fatal(err)
	}
	other, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "other"})
	if err != nil {
		t.Fatal(err)
	}
	mine, err := store.CreateManagedRealtimeEndpointIfUnderQuota(ctx, state.ManagedRealtimeEndpoint{AppID: owned.ID, AccountID: acct.ID, Enabled: true}, 4, 4)
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := store.CreateManagedRealtimeEndpointIfUnderQuota(ctx, state.ManagedRealtimeEndpoint{AppID: other.ID, AccountID: acct.ID, Enabled: true}, 4, 4)
	if err != nil {
		t.Fatal(err)
	}
	s := &Service{Host: fakeHost{store: store}}
	cases := []struct {
		name, query, endpoint, errorMessage string
	}{
		{name: "defaults to the owned endpoint", endpoint: mine.ID},
		{name: "selects the owned endpoint", query: "?endpoint_id=" + mine.ID, endpoint: mine.ID},
		{name: "rejects another app's endpoint", query: "?endpoint_id=" + foreign.ID, errorMessage: "Endpoint is unavailable."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data := dashboard.RealtimeInspectorData{Slug: owned.Slug, After: "0"}
			s.LoadInspector(httptest.NewRequest("GET", "/dashboard/apps/owned/realtime"+tc.query, nil), acct, owned, &data)
			if len(data.Endpoints) != 1 || data.Endpoints[0].ID != mine.ID {
				t.Fatalf("endpoints = %+v, want only %s", data.Endpoints, mine.ID)
			}
			if data.EndpointID != tc.endpoint || data.ErrorMessage != tc.errorMessage {
				t.Fatalf("endpoint=%q error=%q, want %q %q", data.EndpointID, data.ErrorMessage, tc.endpoint, tc.errorMessage)
			}
		})
	}
}

func TestInspectorHelpers(t *testing.T) {
	data := &dashboard.RealtimeInspectorData{Slug: "app", EndpointID: "ep", Channel: "orders", GroupName: "billing"}
	if got := inspectorDeadLetterURL(data, "0"); got != "" {
		t.Fatalf("dead-letter URL for sequence 0 = %q, want empty", got)
	}
	if got := inspectorDeadLetterURL(data, "5"); !strings.Contains(got, "after=4") || !strings.Contains(got, "group=billing") || !strings.HasSuffix(got, "#dead-letter-5") {
		t.Fatalf("dead-letter URL = %q", got)
	}
	revoked := time.Now()
	for _, tc := range []struct {
		revokedAt *time.Time
		expiresAt time.Time
		want      string
	}{
		{nil, time.Now().Add(time.Hour), "Active"},
		{nil, time.Now().Add(-time.Second), "Expired"},
		{&revoked, time.Now().Add(time.Hour), "Revoked"},
	} {
		if got := credentialStatus(tc.revokedAt, tc.expiresAt); got != tc.want {
			t.Errorf("credentialStatus = %q, want %q", got, tc.want)
		}
	}
	past := time.Now().Add(-time.Minute)
	expired := projectInspectorMessage(state.ManagedRealtimeChannelMessage{Sequence: 3, Data: []byte(`{"secret":1}`), Metadata: map[string]string{"k": "v"}, ExpiresAt: &past})
	if !expired.Expired || expired.Payload != "" || expired.Metadata != "" {
		t.Fatalf("expired message leaked content: %+v", expired)
	}
}
