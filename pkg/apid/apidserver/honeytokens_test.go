package apidserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestHoneytokenPresented pins ADR 20261024014500 end to end: a decoy API
// key gets the ordinary 401 and leaves a security.honeytoken_presented
// audit event naming the decoy; an unknown key leaves none.
func TestHoneytokenPresented(t *testing.T) {
	t.Cleanup(apidserver.WithTestRecipient(t)) // loads a host HMAC key
	e := apidserver.Setup(t, api.PlanPro)
	store := *e.Store()
	ctx := context.Background()
	decoy, _, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	digest, err := state.HoneytokenDigest(apidserver.HostHMACKeyBytes(), decoy)
	if err != nil {
		t.Fatal(err)
	}
	h, err := store.CreateHoneytoken(ctx, state.HoneytokenKindAPIKey, digest, "ht-2026-10-24-a")
	if err != nil {
		t.Fatal(err)
	}
	unknown, _, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}

	presented := func() []map[string]any {
		events, err := store.ListEvents(ctx, "", 1000)
		if err != nil {
			t.Fatal(err)
		}
		var out []map[string]any
		for _, ev := range events {
			if ev.Kind != "security.honeytoken_presented" {
				continue
			}
			var data map[string]any
			if err := json.Unmarshal(ev.Data, &data); err != nil {
				t.Fatal(err)
			}
			out = append(out, data)
		}
		return out
	}

	if rec := e.Do(t, http.MethodGet, "/v1/apps", nil, map[string]string{"Authorization": "Bearer " + unknown}); rec.Code != http.StatusUnauthorized {
		t.Fatalf("unknown key: status = %d, want 401", rec.Code)
	}
	if got := presented(); len(got) != 0 {
		t.Fatalf("an unknown key was reported as a honeytoken: %+v", got)
	}

	rec := e.Do(t, http.MethodGet, "/v1/apps", nil, map[string]string{"Authorization": "Bearer " + decoy, "User-Agent": "exfil-tool/1.0"})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("honeytoken: status = %d, want the ordinary 401", rec.Code)
	}
	got := presented()
	if len(got) != 1 {
		t.Fatalf("honeytoken events = %+v, want one", got)
	}
	if got[0]["honeytoken_id"] != h.ID || got[0]["label"] != h.Label || got[0]["user_agent"] != "exfil-tool/1.0" || got[0]["path"] != "/v1/apps" {
		t.Fatalf("honeytoken event = %+v", got[0])
	}
}
