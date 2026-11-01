package state_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type tlsAllowlistTestStore interface {
	state.CustomDomainTLSAllowedHostReader
	state.CustomDomainTLSAllowedHostStore
	CreateCustomDomain(ctx context.Context, domain, appID, token string) (state.CustomDomain, error)
	DeleteCustomDomain(ctx context.Context, domain string) error
}

// TestCustomDomainTLSAllowedHosts pins ADR-520 amendment 2 on both stores: no
// list admits every name, a list admits only its names (and the doctor's
// probe host), clearing restores the open default, and the list goes with
// its wildcard.
func TestCustomDomainTLSAllowedHosts(t *testing.T) {
	stores := map[string]func(t *testing.T) (tlsAllowlistTestStore, context.Context, string){
		"mem": func(t *testing.T) (tlsAllowlistTestStore, context.Context, string) {
			return state.NewMemStore(), context.Background(), uuid.NewString()
		},
		"pg": func(t *testing.T) (tlsAllowlistTestStore, context.Context, string) {
			s, ctx, _, app, _ := pgCoverageFixture(t)
			return s, ctx, app.ID
		},
	}
	for name, open := range stores {
		t.Run(name, func(t *testing.T) {
			s, ctx, appID := open(t)
			suffix := uuid.NewString()[:8] + ".example.com"
			wildcard := "*." + suffix
			if _, err := s.CreateCustomDomain(ctx, wildcard, appID, "tok"); err != nil {
				t.Fatal(err)
			}
			allowed := func(host string) bool {
				t.Helper()
				ok, err := s.CustomDomainTLSHostAllowed(ctx, wildcard, host)
				if err != nil {
					t.Fatalf("allowed %s: %v", host, err)
				}
				return ok
			}
			if names, err := s.CustomDomainTLSAllowedHosts(ctx, wildcard); err != nil || len(names) != 0 {
				t.Fatalf("fresh wildcard allowlist = %v, %v; want empty", names, err)
			}
			if !allowed("random."+suffix) || !allowed("a.b."+suffix) {
				t.Fatal("a wildcard without an allowlist must admit every name")
			}

			if err := s.SetCustomDomainTLSAllowedHosts(ctx, wildcard, []string{"API", "app", "eu.shop." + suffix, "app"}); err != nil {
				t.Fatal(err)
			}
			if names, err := s.CustomDomainTLSAllowedHosts(ctx, wildcard); err != nil || !reflect.DeepEqual(names, []string{"api", "app", "eu.shop"}) {
				t.Fatalf("allowlist = %v, %v; want sorted, lowercased, de-duplicated names", names, err)
			}
			for host, want := range map[string]bool{
				"app." + suffix:     true,
				"APP." + suffix:     true,
				"eu.shop." + suffix: true,
				"shop." + suffix:    false,
				"x.app." + suffix:   false,
				"random." + suffix:  false,
			} {
				if got := allowed(host); got != want {
					t.Errorf("allowed(%s) = %v, want %v", host, got, want)
				}
			}
			probe, _ := state.WildcardProbeHost(wildcard)
			if !allowed(probe) {
				t.Fatalf("the doctor's probe host %s must stay allowed", probe)
			}

			if err := s.SetCustomDomainTLSAllowedHosts(ctx, wildcard, nil); err != nil {
				t.Fatal(err)
			}
			if !allowed("random." + suffix) {
				t.Fatal("clearing the allowlist must admit every name again")
			}

			if _, err := s.CustomDomainTLSHostAllowed(ctx, wildcard, "x.other.com"); !errors.Is(err, state.ErrInvalidArgument) {
				t.Fatalf("host outside the wildcard = %v, want ErrInvalidArgument", err)
			}
			if err := s.SetCustomDomainTLSAllowedHosts(ctx, wildcard, []string{"bad_name"}); !errors.Is(err, state.ErrInvalidArgument) {
				t.Fatalf("invalid name = %v, want ErrInvalidArgument", err)
			}
			if err := s.SetCustomDomainTLSAllowedHosts(ctx, "*.missing-"+suffix, []string{"app"}); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("missing wildcard = %v, want ErrNotFound", err)
			}

			if err := s.SetCustomDomainTLSAllowedHosts(ctx, wildcard, []string{"app"}); err != nil {
				t.Fatal(err)
			}
			if err := s.DeleteCustomDomain(ctx, wildcard); err != nil {
				t.Fatal(err)
			}
			if _, err := s.CreateCustomDomain(ctx, wildcard, appID, "tok2"); err != nil {
				t.Fatal(err)
			}
			if !allowed("random." + suffix) {
				t.Fatal("a re-created wildcard must not inherit the deleted one's allowlist")
			}
		})
	}
}

func TestNormalizeWildcardHostNames(t *testing.T) {
	for _, tc := range []struct {
		names   []string
		want    []string
		wantErr bool
	}{
		{[]string{" App ", "api.example.com", "eu.shop"}, []string{"api", "app", "eu.shop"}, false},
		{[]string{"*"}, nil, true},
		{[]string{"*.app"}, nil, true},
		{[]string{""}, nil, true},
		{[]string{"-app"}, nil, true},
		{[]string{"app-"}, nil, true},
		{[]string{"a..b"}, nil, true},
		{[]string{"under_score"}, nil, true},
	} {
		got, err := state.NormalizeWildcardHostNames("*.example.com", tc.names)
		if (err != nil) != tc.wantErr || !reflect.DeepEqual(got, tc.want) {
			t.Errorf("NormalizeWildcardHostNames(%q) = %q, %v; want %q (error %v)", tc.names, got, err, tc.want, tc.wantErr)
		}
	}
	if _, err := state.NormalizeWildcardHostNames("example.com", []string{"app"}); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("exact domain = %v, want ErrInvalidArgument", err)
	}
	many := make([]string, api.CustomDomainTLSAllowedHostsMax+1)
	for i := range many {
		many[i] = fmt.Sprintf("h%d", i)
	}
	if _, err := state.NormalizeWildcardHostNames("*.example.com", many); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("%d names = %v, want ErrInvalidArgument", len(many), err)
	}
	if _, err := state.NormalizeWildcardHostNames("*.example.com", many[:api.CustomDomainTLSAllowedHostsMax]); err != nil {
		t.Fatalf("%d names refused: %v", api.CustomDomainTLSAllowedHostsMax, err)
	}
}
