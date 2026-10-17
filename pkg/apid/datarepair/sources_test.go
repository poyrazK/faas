package datarepair

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	core "github.com/onebox-faas/faas/pkg/datarepair"
)

func TestRepairSourceRemoteScopeBoundsAndRedirects(t *testing.T) {
	scope := core.Scope{AccountID: uuid.NewString(), AppID: uuid.NewString(), Environment: "production"}
	var destinationCalls int
	destination := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { destinationCalls++ }))
	defer destination.Close()
	for _, mode := range []string{"wrong_scope", "oversized", "redirect", "too_many", "invalid"} {
		t.Run(mode, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer "+strings.Repeat("x", 32) {
					t.Error("executor credential missing")
				}
				if r.URL.Path != "/v1/data-repair/sources" {
					t.Error("discovery left the source endpoint")
				}
				switch mode {
				case "wrong_scope":
					other := scope
					other.PlatformTenantID = uuid.NewString()
					_ = json.NewEncoder(w).Encode(core.SourcePage{Scope: other, Items: []core.Source{}})
				case "oversized":
					_, _ = w.Write([]byte(strings.Repeat("x", api.DataRepairSourceMaxResponseBytes+1)))
				case "redirect":
					http.Redirect(w, r, destination.URL, 307)
				case "too_many":
					_ = json.NewEncoder(w).Encode(core.SourcePage{Scope: scope, Items: make([]core.Source, 2)})
				case "invalid":
					w.WriteHeader(http.StatusBadRequest)
				}
			}))
			defer server.Close()
			remote, err := NewRemote(server.URL, strings.Repeat("x", 32))
			if err != nil {
				t.Fatal(err)
			}
			_, err = remote.Sources(t.Context(), scope, core.SourceQuery{Limit: 1})
			want := core.ErrUnavailable
			if mode == "invalid" {
				want = core.ErrInvalid
			}
			if !errors.Is(err, want) || destinationCalls != 0 {
				t.Fatalf("unsafe metadata response accepted: %v, redirected=%d", err, destinationCalls)
			}
		})
	}
}

func TestRepairSourceBindingRequiresExactUnambiguousScope(t *testing.T) {
	scope := core.Scope{AccountID: uuid.NewString(), AppID: uuid.NewString(), Environment: "production", PlatformTenantID: uuid.NewString()}
	first := Binding{Scope: scope, Identity: "first", Executor: &core.Engine{}}
	second := first
	second.Scope.PlatformTenantID = uuid.NewString()
	managed := &Managed{Bindings: []Binding{first, second}}
	if _, err := managed.sourceBinding(scope.AccountID, scope.AppID, url.Values{}); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("ambiguous customer silently selected: %v", err)
	}
	values := url.Values{"environment": {scope.Environment}, "customer": {scope.PlatformTenantID}}
	binding, err := managed.sourceBinding(scope.AccountID, scope.AppID, values)
	if err != nil || binding.Scope != scope {
		t.Fatalf("exact configured scope unavailable: %+v %v", binding, err)
	}
	if _, err := managed.sourceBinding(uuid.NewString(), scope.AppID, values); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("binding crossed account: %v", err)
	}
	if _, err := managed.sourceBinding(scope.AccountID, uuid.NewString(), values); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("binding crossed application: %v", err)
	}
	q := core.SourceQuery{Cursor: core.EncodeSourceCursor(boundSourceCursor{Binding: "old-generation", Cursor: "prior-page"})}
	if _, err := managed.sourcePage(t.Context(), first, q); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("continuation survived binding replacement: %v", err)
	}
}
