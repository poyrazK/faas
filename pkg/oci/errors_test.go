package oci

import (
	"errors"
	"fmt"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// TestIsImageTerminal_Sentinels is the table-driven round-trip for
// pkg/api.SentinelToCode. Asserts that each of the three puller-side
// sentinels from ADR-021 returns true from IsImageTerminal, that
// errors.Is matches both bare and %w-wrapped forms, and that the
// legacy ErrEgressDenied continues to be terminal (backwards
// compat with every pkg/oci consumer that already checks it).
func TestIsImageTerminal_Sentinels(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"ErrImageNotFound bare", ErrImageNotFound, true},
		{"ErrImageNotFound wrapped", fmt.Errorf("pull failed: %w", ErrImageNotFound), true},
		{"ErrImageEgressDenied bare", ErrImageEgressDenied, true},
		{"ErrImageEgressDenied wrapped", fmt.Errorf("dial failed: %w", ErrImageEgressDenied), true},
		{"ErrImageManifestInvalid bare", ErrImageManifestInvalid, true},
		{"ErrImageManifestInvalid wrapped", fmt.Errorf("parse: %w", ErrImageManifestInvalid), true},
		{"legacy ErrEgressDenied bare", ErrEgressDenied, true},
		{"legacy ErrEgressDenied wrapped", fmt.Errorf("egress: %w", ErrEgressDenied), true},
		{"nil", nil, false},
		{"plain string error", errors.New("some unrelated failure"), false},
		{"wrapped plain error", fmt.Errorf("ctx: %w", errors.New("inner")), false},
		{"double-wrapped sentinel", fmt.Errorf("outer: %w", fmt.Errorf("inner: %w", ErrImageNotFound)), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsImageTerminal(tt.err); got != tt.want {
				t.Errorf("IsImageTerminal(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

// TestSentinels_AreDistinct ensures the three sentinels are not
// aliases for one another — errors.Is(err, X) must be false when X
// != Y, so SentinelToCode can branch deterministically.
func TestSentinels_AreDistinct(t *testing.T) {
	if errors.Is(ErrImageNotFound, ErrImageEgressDenied) {
		t.Error("ErrImageNotFound must not be ErrImageEgressDenied")
	}
	if errors.Is(ErrImageNotFound, ErrImageManifestInvalid) {
		t.Error("ErrImageNotFound must not be ErrImageManifestInvalid")
	}
	if errors.Is(ErrImageEgressDenied, ErrImageManifestInvalid) {
		t.Error("ErrImageEgressDenied must not be ErrImageManifestInvalid")
	}
}

// TestSentinelToCode is the table-driven round-trip for the
// sentinel → pkg/api code mapping (ADR-021). Every sentinel wraps
// cleanly through one and two layers of %w; plain errors return
// ("", false) so the imaged handler falls through to its generic
// failure path.
func TestSentinelToCode(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		wantCode string
		wantOK   bool
	}{
		{"ErrImageNotFound bare", ErrImageNotFound, api.CodeImageNotFound, true},
		{"ErrImageNotFound wrapped", fmt.Errorf("pull failed: %w", ErrImageNotFound), api.CodeImageNotFound, true},
		{"ErrImageEgressDenied bare", ErrImageEgressDenied, api.CodeImageEgressDenied, true},
		{"ErrImageEgressDenied wrapped", fmt.Errorf("dial failed: %w", ErrImageEgressDenied), api.CodeImageEgressDenied, true},
		{"ErrImageManifestInvalid bare", ErrImageManifestInvalid, api.CodeImageManifestInvalid, true},
		{"ErrImageManifestInvalid wrapped", fmt.Errorf("parse: %w", ErrImageManifestInvalid), api.CodeImageManifestInvalid, true},
		{"double-wrapped", fmt.Errorf("outer: %w", fmt.Errorf("inner: %w", ErrImageNotFound)), api.CodeImageNotFound, true},
		{"legacy ErrEgressDenied bare", ErrEgressDenied, "", false},
		{"nil", nil, "", false},
		{"plain error", errors.New("some unrelated failure"), "", false},
		{"wrapped plain error", fmt.Errorf("ctx: %w", errors.New("inner")), "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotCode, gotOK := SentinelToCode(tt.err)
			if gotCode != tt.wantCode || gotOK != tt.wantOK {
				t.Errorf("SentinelToCode(%v) = (%q, %v), want (%q, %v)",
					tt.err, gotCode, gotOK, tt.wantCode, tt.wantOK)
			}
		})
	}
}
