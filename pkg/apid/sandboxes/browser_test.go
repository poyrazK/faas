// adr: 20261116182615 §1 — browser: true boots a preset session with the
// browser running, needs SandboxBrowserRAMMBMin and the operator's switch,
// and exposes the browser's DevTools port privately with the session when
// previews are on.
package sandboxes_test

import (
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	agentstore "github.com/onebox-faas/faas/pkg/state/agents"
)

func TestCreateBrowserSession(t *testing.T) {
	f := newFixture(t)
	var p api.Problem
	if code := f.call(t, f.h.Create, pro(), http.MethodPost, "/v1/sandboxes", `{"preset":"codex","browser":true}`, "", &p); code != http.StatusUnprocessableEntity ||
		p.Code != api.CodeSandboxUnsupported {
		t.Fatalf("browser with the switch off = %d %q", code, p.Code)
	}
	f.h.Browser = true
	for name, body := range map[string]string{
		"too little RAM": `{"preset":"codex","browser":true,"ram_mb":512}`,
		"an image":       `{"image":"ghcr.io/acme/box","browser":true}`,
	} {
		if code := f.call(t, f.h.Create, pro(), http.MethodPost, "/v1/sandboxes", body, "", &p); code != http.StatusBadRequest {
			t.Errorf("%s: create = %d %q, want 400", name, code, p.Code)
		}
	}
	if len(f.store.sessions) != 0 {
		t.Fatalf("a refused request created %d sessions", len(f.store.sessions))
	}

	// Without preview hosts the browser runs for the session's own tools.
	if _, code := f.create(t, pro(), `{"preset":"codex","browser":true}`); code != http.StatusCreated ||
		!f.store.admitted.Browser || len(f.store.admitted.Ports) != 0 {
		t.Fatalf("browser without previews = %d, admitted %+v", code, f.store.admitted)
	}
	f.h.PreviewDomain = previewDomain
	if _, code := f.create(t, pro(), `{"preset":"codex","browser":true,"ports":[{"port":3000}]}`); code != http.StatusCreated {
		t.Fatalf("browser with previews = %d", code)
	}
	got := f.store.admitted.Ports
	if len(got) != 2 || got[1] != (agentstore.SandboxPort{Port: api.SandboxBrowserCDPPort, Visibility: api.SandboxPortPrivate}) {
		t.Fatalf("admitted ports %+v; want 3000 and the private DevTools port", got)
	}
	if _, code := f.create(t, pro(), `{"preset":"codex","browser":true,"ports":[{"port":9222,"visibility":"private"}]}`); code != http.StatusCreated ||
		len(f.store.admitted.Ports) != 1 {
		t.Fatalf("a listed DevTools port is exposed twice: %d %+v", code, f.store.admitted.Ports)
	}
}
