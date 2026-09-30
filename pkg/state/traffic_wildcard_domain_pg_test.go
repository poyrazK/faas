//go:build !no_pg

// adr: 375
package state

import (
	"errors"
	"reflect"
	"testing"
)

func TestPgTrafficWildcardDomainLookupUsesLiteralSuffix(t *testing.T) {
	store, pool, _, app := trafficHostPGFixture(t)
	for _, domain := range []string{"*.example.test", "*.nested.example.test", "*.under_score.example.test", "*.percent%.example.test", "*.spaces.example.test\u00a0", "*.tie.example.test\t", "*.TIE.EXAMPLE.TEST\n", "*.☃.example.test", "UPPER.EXAMPLE.TEST"} {
		if _, err := pool.Exec(t.Context(), `INSERT INTO custom_domains(domain,app_id,challenge_token) VALUES($1,$2,'private-token')`, domain, app.ID); err != nil {
			t.Fatal(err)
		}
	}
	for _, test := range []struct{ host, domain string }{
		{"api.example.test", "*.example.test"},
		{"api.nested.example.test", "*.nested.example.test"},
		{" API.NESTED.EXAMPLE.TEST.\u2003", "*.nested.example.test"},
		{"example.test", ""}, {"badexample.test", ""},
		{"*.example.test", ""}, {"api*.example.test", ""},
		{"api.under_score.example.test", "*.under_score.example.test"},
		{"api.underXscore.example.test", "*.example.test"},
		{"api.percent%.example.test", "*.percent%.example.test"},
		{"api.percent123.example.test", "*.example.test"},
		{"api.spaces.example.test", "*.spaces.example.test\u00a0"},
		{"api.tie.example.test", "*.TIE.EXAMPLE.TEST\n"},
		{".example.test", "*.example.test"},
		{"api.example.test..", ""},
		{"api.☃.example.test", "*.☃.example.test"},
	} {
		t.Run(test.host, func(t *testing.T) {
			got, err := store.WildcardDomainForHost(t.Context(), test.host)
			if test.domain == "" {
				if !errors.Is(err, ErrNotFound) {
					t.Fatalf("unexpected wildcard claim: domain=%q err=%v", got.Domain, err)
				}
			} else {
				full, fullErr := store.DomainByName(t.Context(), test.domain)
				if err != nil || fullErr != nil || !reflect.DeepEqual(got, full) || got.Verified() {
					t.Fatalf("most-specific reservation changed full row: domain=%q err=%v full=%v", got.Domain, err, fullErr)
				}
				if !WildcardMatchesHost(test.domain, test.host) {
					t.Fatal("SQL wildcard differs from shared host language")
				}
			}
			if err := store.WithPublicHostPolicySnapshot(t.Context(), func(reader PublicHostPolicyReader) error {
				row, err := reader.WildcardDomainForHost(t.Context(), test.host)
				if test.domain == "" {
					if !errors.Is(err, ErrNotFound) {
						t.Fatalf("snapshot widened wildcard: %q/%v", row.Domain, err)
					}
				} else if err != nil || row.Domain != test.domain || row.AppID != app.ID || row.Verified() || row.ChallengeToken != "" {
					t.Fatalf("snapshot wildcard changed binding or exposed proof: %q/%v", row.Domain, err)
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
	if err := store.WithPublicHostPolicySnapshot(t.Context(), func(reader PublicHostPolicyReader) error {
		row, err := reader.DomainByName(t.Context(), "upper.example.test")
		if err != nil || row.Domain != "UPPER.EXAMPLE.TEST" {
			t.Fatalf("snapshot exact lookup lost citext identity: %q/%v", row.Domain, err)
		}
		if reserved, err := reader.PublicHostReserved(t.Context(), "", "upper.example.test"); err != nil || !reserved {
			t.Fatalf("case-insensitive claim lost its reservation: %v/%v", reserved, err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if matched, err := store.MarkDomainVerifiedIfChallenge(t.Context(), "upper.example.test", "private-token"); err != nil || !matched {
		t.Fatalf("case-insensitive challenge write missed its claim: %v/%v", matched, err)
	}
	if err := store.MarkDomainVerified(t.Context(), "upper.example.test"); err != nil {
		t.Fatalf("plain case-insensitive verification: %v", err)
	}
}
