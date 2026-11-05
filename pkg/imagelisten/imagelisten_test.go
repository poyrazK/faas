package imagelisten

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/oci"
)

type fakePuller struct {
	configs map[string]oci.ImageConfig
	calls   map[string]int
}

func (f *fakePuller) PullImageConfig(_ context.Context, ref string) (oci.ImageConfig, error) {
	f.calls[ref]++
	cfg, ok := f.configs[ref]
	if !ok {
		return oci.ImageConfig{}, errors.New("unauthorized")
	}
	return cfg, nil
}

func TestListening(t *testing.T) {
	puller := &fakePuller{calls: map[string]int{}, configs: map[string]oci.ImageConfig{
		"ghcr.io/x/api:1":    {ExposedPorts: map[string]struct{}{"8000/tcp": {}}},
		"ghcr.io/x/bare:1":   {ExposedPorts: map[string]struct{}{"9000": {}}},
		"ghcr.io/x/dns:1":    {ExposedPorts: map[string]struct{}{"53/udp": {}}},
		"ghcr.io/x/worker:1": {},
	}}
	now := time.Unix(1_000_000, 0)
	lookup := New(puller, time.Second, time.Minute)
	lookup.now = func() time.Time { return now }

	for _, tc := range []struct {
		image          string
		listens, known bool
	}{
		{"ghcr.io/x/api:1", true, true},
		{"ghcr.io/x/bare:1", true, true},
		{"ghcr.io/x/dns:1", false, true},
		{"ghcr.io/x/worker:1", false, true},
		{"registry.example.com/private/api:1", false, false},
	} {
		listens, known := lookup.Listening(tc.image)
		if listens != tc.listens || known != tc.known {
			t.Errorf("%s = listens %t known %t, want %t %t", tc.image, listens, known, tc.listens, tc.known)
		}
	}

	// A definite answer is reused within the TTL; an unknown one is asked again.
	lookup.Listening("ghcr.io/x/api:1")
	lookup.Listening("registry.example.com/private/api:1")
	if puller.calls["ghcr.io/x/api:1"] != 1 || puller.calls["registry.example.com/private/api:1"] != 2 {
		t.Fatalf("calls = %v; want the definite answer cached and the unknown one retried", puller.calls)
	}
	now = now.Add(2 * time.Minute)
	lookup.Listening("ghcr.io/x/api:1")
	if puller.calls["ghcr.io/x/api:1"] != 2 {
		t.Fatalf("calls = %v; want the answer refreshed after the TTL", puller.calls)
	}
}
