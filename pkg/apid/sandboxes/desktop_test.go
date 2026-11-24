// adr: 20261124075005 §1, §3a, §5 — preset desktop boots the platform's
// desktop drive, named by its pinned image digest, as an image session on
// Pro and Scale, with its screen and at least
// SandboxDesktopRAMMBMin, and exposes its interactive and view-only noVNC
// ports privately when previews are on.
package sandboxes_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	agentstore "github.com/onebox-faas/faas/pkg/state/agents"
)

const desktopImage = "ghcr.io/onebox-faas/sandbox-desktop@sha256:" + "cd0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcd"

func TestCreateDesktop(t *testing.T) {
	f := newFixture(t)
	var ensured []api.SandboxImageSource
	f.h.EnsureImage = func(_ context.Context, _ state.Account, src api.SandboxImageSource) (string, string, *api.Problem) {
		ensured = append(ensured, src)
		return "11111111-1111-1111-1111-111111111111", src.Image, nil
	}
	var p api.Problem
	if code := f.call(t, f.h.Create, pro(), http.MethodPost, "/v1/sandboxes", `{"preset":"desktop"}`, "", &p); code != http.StatusUnprocessableEntity ||
		p.Code != api.CodeSandboxUnsupported {
		t.Fatalf("without the image = %d %+v", code, p)
	}
	f.h.DesktopImage = "ghcr.io/onebox-faas/sandbox-desktop:1"
	if code := f.call(t, f.h.Create, pro(), http.MethodPost, "/v1/sandboxes", `{"preset":"desktop"}`, "", &p); code != http.StatusUnprocessableEntity ||
		p.Code != api.CodeSandboxUnsupported {
		t.Fatalf("with an unpinned image = %d %+v", code, p)
	}
	f.h.DesktopImage = desktopImage

	sb, code := f.create(t, pro(), `{"preset":"desktop"}`)
	a := f.store.admitted
	if code != http.StatusCreated || sb.Preset != api.SandboxPresetImage || sb.Image != desktopImage ||
		a.DesktopResolution != api.SandboxDesktopDefaultResolution || a.RAMMB != api.SandboxDesktopDefaultRAMMB || len(a.Ports) != 0 {
		t.Fatalf("create = %d %+v, admitted %+v", code, sb, a)
	}
	// §3a: the platform drive, not a per-account image-run job.
	if len(ensured) != 0 || a.ImageRef != desktopImage || a.ImageJobID != "" ||
		a.DesktopImageKey != "base/sandbox-desktop-cd0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcd.ext4" {
		t.Fatalf("ensured %+v, admitted image %q job %q drive %q", ensured, a.ImageRef, a.ImageJobID, a.DesktopImageKey)
	}

	if _, code := f.create(t, pro(), `{"preset":"desktop","ram_mb":1536}`); code != http.StatusCreated || f.store.admitted.RAMMB != api.SandboxDesktopRAMMBMin {
		t.Fatalf("desktop at the RAM minimum = %d, admitted %d MB", code, f.store.admitted.RAMMB)
	}

	f.h.PreviewDomain = previewDomain
	if _, code := f.create(t, pro(), `{"preset":"desktop","resolution":"1280x800","ports":[{"port":3000},{"port":6081}]}`); code != http.StatusCreated {
		t.Fatalf("desktop with previews = %d", code)
	}
	want := []agentstore.SandboxPort{
		{Port: 3000, Visibility: api.SandboxPortPrivate},
		{Port: api.SandboxDesktopViewOnlyPort, Visibility: api.SandboxPortPrivate},
		{Port: api.SandboxDesktopViewPort, Visibility: api.SandboxPortPrivate},
	}
	if a := f.store.admitted; a.DesktopResolution != "1280x800" || len(a.Ports) != len(want) ||
		a.Ports[0] != want[0] || a.Ports[1] != want[1] || a.Ports[2] != want[2] {
		t.Fatalf("admitted %+v; want 1280x800 and ports %+v", a, want)
	}
}

func TestCreateDesktopRefused(t *testing.T) {
	f := newFixture(t)
	f.h.DesktopImage = desktopImage
	f.h.PreviewDomain = previewDomain
	f.h.EnsureImage = func(_ context.Context, _ state.Account, src api.SandboxImageSource) (string, string, *api.Problem) {
		t.Errorf("a refused request cached %q", src.Image)
		return "", "", nil
	}
	hobby := state.Account{ID: pro().ID, Plan: api.PlanHobby}
	for _, tc := range []struct {
		name string
		acct state.Account
		body string
		code int
	}{
		{"hobby plan", hobby, `{"preset":"desktop"}`, http.StatusForbidden},
		{"an image", pro(), `{"preset":"desktop","image":"ghcr.io/acme/box"}`, http.StatusBadRequest},
		{"a template", pro(), `{"preset":"desktop","template":"mine"}`, http.StatusBadRequest},
		{"browser", pro(), `{"preset":"desktop","browser":true}`, http.StatusBadRequest},
		{"too little RAM", pro(), `{"preset":"desktop","ram_mb":1535}`, http.StatusBadRequest},
		{"too wide", pro(), `{"preset":"desktop","resolution":"2560x1080"}`, http.StatusBadRequest},
		{"too short", pro(), `{"preset":"desktop","resolution":"1024x400"}`, http.StatusBadRequest},
		{"not WxH", pro(), `{"preset":"desktop","resolution":"1024*768"}`, http.StatusBadRequest},
		{"leading zero", pro(), `{"preset":"desktop","resolution":"01024x768"}`, http.StatusBadRequest},
		{"signed", pro(), `{"preset":"desktop","resolution":"+1024x768"}`, http.StatusBadRequest},
		{"resolution without desktop", pro(), `{"preset":"codex","resolution":"1024x768"}`, http.StatusBadRequest},
		{"no room for the views", pro(), `{"preset":"desktop","ports":[{"port":3000},{"port":3001},{"port":3002},{"port":3003}]}`, http.StatusForbidden},
	} {
		var p api.Problem
		if code := f.call(t, f.h.Create, tc.acct, http.MethodPost, "/v1/sandboxes", tc.body, "", &p); code != tc.code {
			t.Errorf("%s: create = %d %+v, want %d", tc.name, code, p, tc.code)
		}
	}
	if len(f.store.sessions) != 0 {
		t.Fatalf("a refused request created %d sessions", len(f.store.sessions))
	}
}
