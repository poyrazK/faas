package managedpostgres

import (
	"context"
	"testing"
)

type pinnedHoldProvider struct {
	*fakeProvider
	held, released string
}

func (p *pinnedHoldProvider) HoldForDeletion(_ context.Context, id string) error {
	p.held = id
	return nil
}
func (p *pinnedHoldProvider) ReleaseDeletionHold(_ context.Context, id string) error {
	p.released = id
	return nil
}

func TestPinnedDeletionHoldKeepsDataset(t *testing.T) {
	p := &pinnedHoldProvider{fakeProvider: &fakeProvider{capabilities: testCapabilities()}}
	registry := testRegistry(t, p, nil)
	backend, err := registry.Default(testSpec().Region)
	if err != nil {
		t.Fatal(err)
	}
	svc, err := NewService(registry, NewMemoryStore(), ServiceOptions{})
	if err != nil {
		t.Fatal(err)
	}
	d := Database{BackendID: backend.ID, BackendFingerprint: backend.Fingerprint, ProviderResourceID: "quiet-river-12345678", DataResourceID: "quiet-river-12345678/br-original-123"}
	if err := svc.deletionHold(t.Context(), d, true); err != nil {
		t.Fatal(err)
	}
	if err := svc.deletionHold(t.Context(), d, false); err != nil {
		t.Fatal(err)
	}
	if p.held != d.DataResourceID || p.released != d.DataResourceID {
		t.Fatalf("pinned dataset=%s, hold targeted=%s, release targeted=%s", d.DataResourceID, p.held, p.released)
	}
}
