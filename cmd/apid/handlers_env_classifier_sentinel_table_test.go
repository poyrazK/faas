// Issue #957 — typed-sentinel unit test.
//
// handlers_env.go::runEnvClassifier returns *errEnvClassifier
// sentinels at every failure site; setEnv's audit emit uses
// errors.As to read the Kind discriminator and stamp the
// silent_skip boolean.
//
// silent_skip semantic: true iff the failure bailed BEFORE any
// data_upstreams INSERT was attempted. Only Kind="insert_data_upstream"
// is the post-INSERT failure case; every other Kind fails before
// reaching InsertDataUpstream at runEnvClassifier (uuid_parse at
// L570/L574, classifier.Run at L610, CountDataUpstreamsByApp at
// L634, HostHashOK at L624, port bounds at L666). Consumers that
// key off silent_skip to mean "no DB write happened" now get the
// correct answer for every branch in the closed-vocab.
//
// The integration seam (TestSetEnv_ClassifierFailure_HostHashFailed_
// EmitsAuditEvent) covers the silent-skip branch end-to-end; the
// remaining branches (uuid_parse, port_out_of_range,
// classifier_internal, insert_data_upstream) require pgtest
// (state.MemStore's data_upstreams methods are Postgres-only
// stubs; see pkg/state/memstore_data_upstreams.go:33). This file
// pins the table directly so a future refactor that drifts the
// closed-vocab trips the gate silently otherwise.

package main

import (
	"errors"
	"fmt"
	"testing"
)

func TestErrEnvClassifier_Sentinels(t *testing.T) {
	cases := []struct {
		name           string
		sentinel       *errEnvClassifier
		wantKind       string
		wantSilentSkip bool // silent_skip = (Kind == "host_hash_failed")
	}{
		{
			name:     "uuid_parse",
			sentinel: errClassifierUUIDParse,
			wantKind: "uuid_parse",
			// silent_skip = (Kind != insert_data_upstream).
			// uuid_parse fails BEFORE InsertDataUpstream
			// (runEnvClassifier L570/L574) → silent_skip=true.
			wantSilentSkip: true,
		},
		{
			name:           "host_hash_failed",
			sentinel:       errClassifierHostHashFailed,
			wantKind:       "host_hash_failed",
			wantSilentSkip: true,
		},
		{
			name:     "insert_data_upstream",
			sentinel: errClassifierInsert,
			wantKind: "insert_data_upstream",
			// INSERT was attempted (runEnvClassifier L691
			// returns the error from InsertDataUpstream).
			// silent_skip=false: the row DID touch the DB.
			wantSilentSkip: false,
		},
		{
			name:     "port_out_of_range",
			sentinel: errClassifierPortRange,
			wantKind: "port_out_of_range",
			// Bounds check at L666 trips BEFORE INSERT.
			wantSilentSkip: true,
		},
		{
			name:     "classifier_internal",
			sentinel: errClassifierInternal,
			wantKind: "classifier_internal",
			// Both L610 (classifier.Run) and L634
			// (CountDataUpstreamsByApp) bail BEFORE INSERT.
			wantSilentSkip: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.sentinel == nil {
				t.Fatalf("sentinel %s is nil", tc.name)
			}
			if tc.sentinel.Kind != tc.wantKind {
				t.Errorf("Kind = %q, want %q", tc.sentinel.Kind, tc.wantKind)
			}
			if tc.sentinel.Error() == "" {
				t.Errorf("Error() = empty string")
			}
			// errors.As on the sentinel must succeed against
			// the *errEnvClassifier target.
			var ec *errEnvClassifier
			if !errors.As(tc.sentinel, &ec) {
				t.Errorf("errors.As(sentinel, &*errEnvClassifier) = false")
			}
			if ec.Kind != tc.wantKind {
				t.Errorf("after As: ec.Kind = %q, want %q", ec.Kind, tc.wantKind)
			}
			// silent_skip dispatch mirrors setEnv's audit emit:
			// true iff the failure bailed before
			// InsertDataUpstream was attempted. Only
			// insert_data_upstream is the post-INSERT
			// failure case.
			gotSilentSkip := ec.Kind != errClassifierInsert.Kind
			if gotSilentSkip != tc.wantSilentSkip {
				t.Errorf("silent_skip dispatch = %v, want %v (Kind=%q)",
					gotSilentSkip, tc.wantSilentSkip, ec.Kind)
			}
		})
	}
}

// TestErrEnvClassifier_WrapWithInner checks that wrapping an inner
// error preserves Unwrap() and that errors.As(target) surfaces
// the inner. Used at the runEnvClassifier sites that already had a
// concrete cause (e.g. uuid.Parse, sql.ErrConnDone).
func TestErrEnvClassifier_WrapWithInner(t *testing.T) {
	inner := errors.New("pgx: conn closed")
	wrapped := &errEnvClassifier{
		Kind: errClassifierInternal.Kind,
		Err:  inner,
	}
	if !errors.Is(wrapped, inner) {
		t.Errorf("errors.Is(wrapped, inner) = false; Unwrap() = %v", wrapped.Unwrap())
	}
	if wrapped.Kind != "classifier_internal" {
		t.Errorf("Kind = %q, want classifier_internal", wrapped.Kind)
	}
	wantMsg := fmt.Sprintf("env-classifier: classifier_internal: %s", inner.Error())
	if wrapped.Error() != wantMsg {
		t.Errorf("Error() = %q, want %q", wrapped.Error(), wantMsg)
	}
	// errors.As should find the inner.
	if !errors.Is(wrapped, inner) {
		t.Errorf("errors.Is(wrapped, inner) = false")
	}
}

func TestClassifierFailureMetricReason(t *testing.T) {
	cases := map[string]string{
		"host_hash_failed":     "salt_missing",
		"port_out_of_range":    "port_out_of_range",
		"unknown_kind":         "unknown_kind",
		"uuid_parse":           "internal_error",
		"insert_data_upstream": "internal_error",
		"classifier_internal":  "internal_error",
		"future_failure":       "internal_error",
	}
	for errorClass, want := range cases {
		t.Run(errorClass, func(t *testing.T) {
			if got := classifierFailureMetricReason(errorClass); got != want {
				t.Fatalf("classifierFailureMetricReason(%q) = %q, want %q", errorClass, got, want)
			}
		})
	}
}
