package main

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestDeriveLast4FromHash_UsesCanonicalEightHexFragment(t *testing.T) {
	for _, tc := range []struct {
		name string
		hash string
		want string
	}{
		{name: "full hash", hash: "0123456789abcdef0123456789abcdef", want: "01234567"},
		{name: "short hash", hash: "abc", want: "abc"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := deriveLast4FromHash(tc.hash); got != tc.want {
				t.Fatalf("deriveLast4FromHash(%q) = %q, want %q", tc.hash, got, tc.want)
			}
		})
	}
}

func TestDataUpstreamResponseFromState_UsesEightHexFragment(t *testing.T) {
	now := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	row := state.DataUpstream{
		ID:               uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		Source:           state.DataUpstreamSourceExplicit,
		Scope:            "primary",
		Kind:             state.DataUpstreamKindPostgres,
		Port:             5432,
		HostRedactedHash: "fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210",
		CreatedAt:        now,
		LastSeenAt:       now,
	}
	got := dataUpstreamResponseFromState(row)
	if got.HostLast4 != "fedcba98" {
		t.Fatalf("HostLast4 = %q, want first 8 hash chars", got.HostLast4)
	}
}
