// adr: 20261103163954
package knownhosts

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"
)

type fakeSource struct {
	slugs, domains []string
	err            error
	limit          int
	calls          int
}

func (f *fakeSource) KnownRouteHosts(_ context.Context, limit int) ([]string, []string, error) {
	f.calls++
	f.limit = limit
	return f.slugs, f.domains, f.err
}

func TestSetHas(t *testing.T) {
	s := New(".Gregale.dev")
	s.Replace([]string{"pulse", "Shop"}, []string{"www.Example.com.", "*.wild.example.org", " api.example.net "})
	for _, tc := range []struct {
		host string
		want bool
	}{
		{"pulse.gregale.dev", true},
		{"shop.gregale.dev", true},
		{"pulse.gregale.dev.", true},
		{"other.gregale.dev", false},
		{"a.pulse.gregale.dev", false}, // dotted label is never a slug
		{"gregale.dev", false},
		{"www.example.com", true},
		{"api.example.net", true},
		{"example.com", false},
		// A wildcard would let every random name under it skip the budget.
		{"x.wild.example.org", false},
		{"*.wild.example.org", false},
		// A slug is known only under the apps domain.
		{"pulse", false},
		{"pulse.example.com", false},
	} {
		if got := s.Has(tc.host); got != tc.want {
			t.Errorf("Has(%q) = %v, want %v", tc.host, got, tc.want)
		}
	}
	if got := s.Len(); got != 4 {
		t.Fatalf("Len = %d, want 4 (2 slugs, 2 exact domains)", got)
	}
}

func TestNilAndEmptySetsKnowNothing(t *testing.T) {
	var nilSet *Set
	if nilSet.Has("pulse.gregale.dev") || nilSet.Len() != 0 {
		t.Fatal("a nil Set knows a host")
	}
	if New("gregale.dev").Has("pulse.gregale.dev") {
		t.Fatal("an unrefreshed Set knows a host")
	}
	noApps := New("")
	noApps.Replace([]string{"pulse"}, []string{"www.example.com"})
	if noApps.Has("pulse.gregale.dev") || !noApps.Has("www.example.com") {
		t.Fatal("without an apps domain only custom domains are known")
	}
}

func TestRefreshKeepsPreviousHostsOnError(t *testing.T) {
	s := New("gregale.dev")
	src := &fakeSource{slugs: []string{"pulse"}}
	if err := s.Refresh(context.Background(), src); err != nil {
		t.Fatal(err)
	}
	if src.limit <= 0 {
		t.Fatalf("Refresh passed limit %d, want the configured bound", src.limit)
	}
	src.err = errors.New("database down")
	src.slugs = nil
	if err := s.Refresh(context.Background(), src); err == nil {
		t.Fatal("Refresh hid the source error")
	}
	if !s.Has("pulse.gregale.dev") {
		t.Fatal("a failed refresh dropped the known hosts")
	}
	src.err = nil
	src.slugs = []string{"shop"}
	if err := s.Refresh(context.Background(), src); err != nil {
		t.Fatal(err)
	}
	if s.Has("pulse.gregale.dev") || !s.Has("shop.gregale.dev") {
		t.Fatal("a refresh did not replace the known hosts")
	}
}

func TestRunRefreshesBeforeTheFirstTick(t *testing.T) {
	s := New("gregale.dev")
	src := &fakeSource{slugs: []string{"pulse"}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		Run(ctx, s, src, slog.New(slog.NewTextHandler(io.Discard, nil)))
		close(done)
	}()
	deadline := time.Now().Add(5 * time.Second)
	for !s.Has("pulse.gregale.dev") {
		if time.Now().After(deadline) {
			t.Fatal("Run did not refresh at start")
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	<-done
}
