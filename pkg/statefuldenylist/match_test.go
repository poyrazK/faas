package statefuldenylist

import (
	"strings"
	"testing"
)

// TestMatch_PinsImagedBehaviour pins that the apid-side denylist
// gate (issue #463 / ADR-066 §Decision 4) agrees with the imaged
// runtime gate on every documented reference shape. The match
// table below mirrors pkg/imaged/base_test.go::TestStatefulDenyListMatch
// (or its equivalent) so a future change to one side that drifts
// from the other is caught at PR time.
func TestMatch_PinsImagedBehaviour(t *testing.T) {
	cases := []struct {
		name    string
		ref     string
		wantOK  bool
		wantKey string // expected Set key when wantOK=true
	}{
		// Match cases — these must trip the gate at apid AND at
		// imaged's pull path. The customer's request gets a 403
		// `sidecar_stateful_denied` here before imaged even runs.
		{"bare-postgres", "postgres:16", true, "postgres"},
		{"bare-postgres-sha", "postgres@sha256:" + strings.Repeat("a", 64), true, "postgres"},
		{"tag-and-digest", "docker.io/library/redis:7-alpine@sha256:" + strings.Repeat("a", 64), true, "redis"},
		{"dockerhub-postgres", "docker.io/postgres:16", true, "postgres"},
		{"dockerhub-library-postgres", "docker.io/library/postgres:16", true, "postgres"},
		{"ghcr-postgres-alpine", "ghcr.io/me/postgres:16-alpine", true, "postgres"},
		{"localhost-postgres", "localhost:5000/myrepo/postgres:dev", true, "postgres"},
		{"redis-bare", "redis:7", true, "redis"},
		{"mysql-bare", "mysql:8", true, "mysql"},
		{"mongo-bare", "mongo:7", true, "mongo"},
		{"clickhouse-deep-path", "myreg.example.com/x/y/clickhouse:tag", true, "clickhouse"},

		// No-match cases — the gate MUST NOT trip on these. A
		// false positive here would 403 a legitimate deploy.
		{"empty", "", false, ""},
		{"postgres-fork-not-stateful", "ghcr.io/me/postgres-fork:v1", false, ""},
		{"redis-typo-with-prefix", "ghcr.io/me/myredis:1", false, ""},
		{"library-postgres-as-app-name", "ghcr.io/me/library/postgres-fork:v1", false, ""},
		{"unrelated-app", "ghcr.io/me/myapp@sha256:" + strings.Repeat("b", 64), false, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hint, ok := Match(tc.ref)
			if ok != tc.wantOK {
				t.Fatalf("Match(%q) ok = %v, want %v (hint=%q)", tc.ref, ok, tc.wantOK, hint)
			}
			if ok && hint == "" {
				t.Errorf("Match(%q) returned ok=true but empty hint; Set[%q] entry has no value", tc.ref, tc.wantKey)
			}
		})
	}
}

// TestSet_AllKeysHaveHint pins that every entry in Set carries a
// non-empty remediation hint. The apid error constructor surfaces
// the hint in the RFC 7807 Detail field; an empty hint would render
// an unhelpful "stateless sidecar image is not allowed" message
// with no actionable copy.
//
// Mirrors pkg/imaged/base_test.go::TestStatefulBaseImageDenylist_AllKeysHaveHint.
func TestSet_AllKeysHaveHint(t *testing.T) {
	for name, hint := range Set {
		if hint == "" {
			t.Errorf("Set[%q] has empty hint; remediation copy is part of the wire contract", name)
		}
		if len(Set) < 4 {
			t.Errorf("Set has only %d entries; the year-one denylist is ~8 — review whether new stateful workloads shipped since the last edit", len(Set))
		}
	}
}
