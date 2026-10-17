package hosting

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestPreviewCASIsImmutableAndRevocationTerminal(t *testing.T) {
	backend, f := routeBackend(t)
	site, _ := routeSite(backend.ID())
	id := uuid.NewString()
	p := PreviewRoute{Version: 1, ID: id, Hostname: "p-" + id + ".hosting.example", SiteID: site.ID, CanonicalHostname: site.Hostname, OriginID: backend.ID(), ReleaseID: uuid.NewString(), ExpiresAt: time.Now().Add(time.Hour).Unix(), Generation: 1}
	// A late first publication races revocation on the same missing R2 object.
	f.blockGeneration = 1
	done := make(chan error, 1)
	go func() { done <- backend.PutPreview(t.Context(), p) }()
	select {
	case <-f.blocked:
	case <-time.After(5 * time.Second):
		t.Fatal("first publication did not block")
	}
	revoked := p
	revoked.Generation = 2
	revoked.Revoked = true
	if err := backend.PutPreview(t.Context(), revoked); err != nil {
		t.Fatal(err)
	}
	close(f.release)
	if err := <-done; !errors.Is(err, ErrStaleRoute) {
		t.Fatal("late publisher revived preview", err)
	}
	if err := backend.PutPreview(t.Context(), revoked); err != nil {
		t.Fatal("repair", err)
	}
	for _, change := range []func(*PreviewRoute){func(p *PreviewRoute) { p.ReleaseID = uuid.NewString() }, func(p *PreviewRoute) { p.ExpiresAt++ }} {
		changed := revoked
		change(&changed)
		if err := backend.PutPreview(t.Context(), changed); !errors.Is(err, ErrStaleRoute) {
			t.Fatal("binding changed", err)
		}
	}
	if err := backend.PutPreview(t.Context(), p); !errors.Is(err, ErrStaleRoute) {
		t.Fatal("recreated preview", err)
	}
}
