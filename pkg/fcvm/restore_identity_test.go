package fcvm

import (
	"errors"
	"strings"
	"testing"
)

// ADR-20261031193635 slice 5b: an identity-reload restore passes its resume
// hook only when the guest echoes the digest vmmd served, and only once.
func TestRestoreIdentityExpectation(t *testing.T) {
	digest := strings.Repeat("a", 64)
	other := strings.Repeat("b", 64)
	for _, tc := range []struct {
		name    string
		served  string
		echoed  string
		wantErr bool
	}{
		{"acknowledged", digest, digest, false},
		{"guest echoes another identity", digest, other, true},
		{"older guest echoes nothing", digest, "", true},
		{"guest never fetched the identity", "", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			exp := newRestoreIdentityExpectation()
			m := &Manager{waking: map[string]wakingInstance{"ins-1": {restoreIdentity: exp}, "ins-2": {}}}
			if !m.RestoreIdentityPending("ins-1") || m.RestoreIdentityPending("ins-2") || m.RestoreIdentityPending("gone") {
				t.Fatal("pending reported for the wrong instances")
			}
			if err := m.ServeRestoreIdentity("ins-2", digest); !errors.Is(err, ErrRestoreIdentityNotExpected) {
				t.Fatalf("served an instance without an identity reload: %v", err)
			}
			if tc.served != "" {
				if err := m.ServeRestoreIdentity("ins-1", tc.served); err != nil {
					t.Fatal(err)
				}
			}
			err := checkRestoreIdentityAck(exp, tc.echoed)
			if (err != nil) != tc.wantErr || (err != nil && !errors.Is(err, ErrAfterRestoreHook)) {
				t.Fatalf("checkRestoreIdentityAck = %v; wantErr %v", err, tc.wantErr)
			}
			// One-shot: later resumes of the VM neither ask nor check again.
			if exp.pending() || m.RestoreIdentityPending("ins-1") {
				t.Fatal("expectation still pending after its check")
			}
			if err := checkRestoreIdentityAck(exp, ""); err != nil {
				t.Fatalf("a later resume was checked again: %v", err)
			}
			if err := m.ServeRestoreIdentity("ins-1", digest); !errors.Is(err, ErrRestoreIdentityNotExpected) {
				t.Fatalf("served after the check: %v", err)
			}
		})
	}
	if err := checkRestoreIdentityAck(nil, ""); err != nil {
		t.Fatalf("a restore without identity reload was checked: %v", err)
	}
	if err := newRestoreIdentityExpectation().serve("not-a-digest"); err == nil {
		t.Fatal("served an invalid digest")
	}
}
