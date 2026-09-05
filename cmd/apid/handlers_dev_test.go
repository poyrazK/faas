package main

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestDevSessionCreateRefreshAndDestroy(t *testing.T) {
	e := setup(t, api.PlanHobby)
	project := "gregale-api"
	wantSlug := devSessionSlug(e.acct.ID, project)

	created := e.do(t, "PUT", "/v1/dev/sessions/"+project, api.UpsertDevSessionRequest{}, nil)
	if created.Code != 201 {
		t.Fatalf("create status = %d, want 201: %s", created.Code, created.Body.String())
	}
	var first api.DevSessionResponse
	if err := json.Unmarshal(created.Body.Bytes(), &first); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if first.App.Slug != wantSlug || first.App.URL != "https://"+wantSlug+".gregale.dev" {
		t.Fatalf("unexpected developer app: %+v", first.App)
	}
	if until := time.Until(first.ExpiresAt); until < 23*time.Hour || until > 25*time.Hour {
		t.Fatalf("lease duration = %s, want about 24h", until)
	}
	row, err := e.store.AppBySlug(t.Context(), wantSlug)
	if err != nil {
		t.Fatalf("load developer app: %v", err)
	}
	if row.PreviewOfSlug != project || row.PreviewPrNumber != 0 || row.PreviewPrState != state.PreviewPrStateOpen {
		t.Fatalf("developer preview metadata = %+v", row)
	}

	refreshed := e.do(t, "PUT", "/v1/dev/sessions/"+project, api.UpsertDevSessionRequest{}, nil)
	if refreshed.Code != 200 {
		t.Fatalf("refresh status = %d, want 200: %s", refreshed.Code, refreshed.Body.String())
	}
	var second api.DevSessionResponse
	if err := json.Unmarshal(refreshed.Body.Bytes(), &second); err != nil {
		t.Fatalf("decode refresh response: %v", err)
	}
	if second.App.ID != first.App.ID {
		t.Fatalf("refresh changed app id: first=%s second=%s", first.App.ID, second.App.ID)
	}

	destroyed := e.do(t, "DELETE", "/v1/dev/sessions/"+project, nil, nil)
	if destroyed.Code != 204 {
		t.Fatalf("destroy status = %d, want 204: %s", destroyed.Code, destroyed.Body.String())
	}
	if _, err := e.store.AppBySlug(t.Context(), wantSlug); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("developer app after destroy: err=%v, want ErrNotFound", err)
	}
}

func TestDevSessionRejectsFunctionWithoutRuntime(t *testing.T) {
	e := setup(t, api.PlanHobby)
	rec := e.do(t, "PUT", "/v1/dev/sessions/my-function", api.UpsertDevSessionRequest{Type: "function"}, nil)
	if rec.Code != 400 {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
}

func TestDevSessionSlugStableAndBounded(t *testing.T) {
	a := devSessionSlug("account-a", "a-very-long-project-name-that-needs-truncation")
	b := devSessionSlug("account-a", "a-very-long-project-name-that-needs-truncation")
	c := devSessionSlug("account-b", "a-very-long-project-name-that-needs-truncation")
	if a != b {
		t.Fatalf("slug is not stable: %q != %q", a, b)
	}
	if a == c {
		t.Fatalf("account-scoped slugs collided: %q", a)
	}
	if len(a) > 40 {
		t.Fatalf("slug length = %d, want <= 40: %q", len(a), a)
	}
}
