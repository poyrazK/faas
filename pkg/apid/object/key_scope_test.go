// adr: 20261106114500 — apid issues key-scoped bindings only where the
// gateway that enforces them is released, and only literal prefixes.
package object

import (
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type keyScopeHost struct {
	Host
	released bool
	asked    []string
}

func (h *keyScopeHost) CapabilityEnabledFor(capability string, _ state.Account, _ state.App) bool {
	h.asked = append(h.asked, capability)
	return h.released
}

type hostWithoutCapabilities struct{ Host }

func TestKeyScopedBindingsNeedTheCapability(t *testing.T) {
	acct, app := state.Account{ID: "acct"}, state.App{ID: "app"}

	held := &keyScopeHost{}
	if p := (&Service{Host: held}).keyScopeProblem(acct, app, "segments/L0/"); p == nil || p.Status != http.StatusForbidden || p.Code != "object_storage_key_scopes_unavailable" {
		t.Fatalf("unreleased = %+v, want 403 object_storage_key_scopes_unavailable", p)
	}
	if len(held.asked) != 1 || held.asked[0] != api.ObjectStorageKeyScopesCapability {
		t.Fatalf("capabilities asked = %v", held.asked)
	}
	if p := (&Service{Host: hostWithoutCapabilities{}}).keyScopeProblem(acct, app, "segments/L0/"); p == nil || p.Status != http.StatusForbidden {
		t.Fatalf("a host that cannot decide capabilities = %+v, want a refusal", p)
	}
	released := &Service{Host: &keyScopeHost{released: true}}
	if p := released.keyScopeProblem(acct, app, "segments/L0/"); p != nil {
		t.Fatalf("released = %+v, want admitted", p)
	}
	for _, prefix := range []string{"v1/*/L0/", "a?b", "x[1]", "../up", strings.Repeat("a", api.MaxObjectKeyScopeBytes+1)} {
		if p := released.keyScopeProblem(acct, app, prefix); p == nil || p.Status != http.StatusBadRequest {
			t.Errorf("key_prefix %.20q = %+v, want 400", prefix, p)
		}
	}
}
