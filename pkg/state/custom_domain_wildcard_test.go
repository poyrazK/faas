package state

import (
	"context"
	"errors"
	"testing"
)

func TestWildcardCustomDomainHelpers(t *testing.T) {
	tests := []struct {
		name    string
		domain  string
		host    string
		matches bool
	}{
		{name: "one label", domain: "*.example.com", host: "api.example.com", matches: true},
		{name: "nested labels", domain: "*.example.com", host: "a.b.example.com", matches: true},
		{name: "apex excluded", domain: "*.example.com", host: "example.com", matches: false},
		{name: "look alike excluded", domain: "*.example.com", host: "badexample.com", matches: false},
		{name: "literal wildcard host excluded", domain: "*.example.com", host: "*.example.com", matches: false},
		{name: "more specific", domain: "*.b.example.com", host: "a.b.example.com", matches: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := WildcardMatchesHost(tc.domain, tc.host); got != tc.matches {
				t.Fatalf("WildcardMatchesHost(%q, %q) = %v, want %v", tc.domain, tc.host, got, tc.matches)
			}
		})
	}
	if got := CustomDomainChallengeName("*.example.com"); got != "_faas-verify.example.com" {
		t.Fatalf("challenge name = %q", got)
	}
	if got, ok := WildcardProbeHost("*.example.com"); !ok || got != "www.example.com" {
		t.Fatalf("probe host = %q, %v", got, ok)
	}
}

func TestValidateCustomDomainName(t *testing.T) {
	valid := []string{"example.com", "api.example.com", "*.example.com", "*.co.uk"}
	for _, domain := range valid {
		if err := ValidateCustomDomainName(domain); err != nil {
			t.Errorf("ValidateCustomDomainName(%q): %v", domain, err)
		}
	}
	invalid := []string{"", "localhost", "*.com", "foo.*.example.com", "*.*.example.com", "api.example.com.", "bad label.example.com"}
	for _, domain := range invalid {
		if err := ValidateCustomDomainName(domain); err == nil {
			t.Errorf("ValidateCustomDomainName(%q) = nil, want error", domain)
		}
	}
}

func TestMemStoreWildcardDomainForHostMostSpecific(t *testing.T) {
	ctx := context.Background()
	m := NewMemStore()
	acct, err := m.CreateAccount(ctx, "wildcard@example.com", "pro")
	if err != nil {
		t.Fatal(err)
	}
	app, err := m.CreateApp(ctx, App{AccountID: acct.ID, Slug: "wildcard", Status: AppActive})
	if err != nil {
		t.Fatal(err)
	}
	for _, domain := range []string{"*.example.com", "*.b.example.com"} {
		if _, err := m.CreateCustomDomain(ctx, domain, app.ID, "token"); err != nil {
			t.Fatal(err)
		}
		if err := m.MarkDomainVerified(ctx, domain); err != nil {
			t.Fatal(err)
		}
	}
	got, err := m.WildcardDomainForHost(ctx, "a.b.example.com")
	if err != nil || got.Domain != "*.b.example.com" {
		t.Fatalf("lookup = %+v, %v; want most-specific wildcard", got, err)
	}
	if _, err := m.WildcardDomainForHost(ctx, "example.com"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("apex lookup err = %v, want ErrNotFound", err)
	}
}
