package main

import "testing"

// adr: 198
//
// revisionRefCases is the shared contract for what a `v42` handle means.
// cmd/gregale/deployment_ref_test.go asserts the IDENTICAL table against its
// own parseRevisionRef. The two parsers are deliberately duplicated (cmd/gregale
// must not import cmd/apid), so this table is the thing that stops them
// drifting: a change to one side that is not mirrored fails the other side's
// test. Keep the two tables byte-identical.
//
// Divergence is not hypothetical harm. If the CLI accepted a form the server
// rejected, `gregale rollback --to <form>` would resolve locally for
// `traffic set` and 404 for `rollback`, and the customer would see one handle
// work in one command and not the other.
var revisionRefCases = []struct {
	name   string
	in     string
	want   int
	wantOK bool
}{
	{"lowercase v prefix", "v42", 42, true},
	{"uppercase V prefix", "V42", 42, true},
	{"bare digits", "42", 42, true},
	{"surrounding whitespace is trimmed", "  v42  ", 42, true},
	{"revision one", "v1", 1, true},
	{"large revision", "v100000", 100000, true},

	// A uuid must never parse as a revision: uuids always carry non-digit
	// characters, so Atoi rejects them. This is the case that makes it safe
	// to accept both forms on the same argument.
	{"uuid is not a revision", "8f14e45fceea467a9c8e9b0e21c6d5a1", 0, false},
	// A 32-char ALL-NUMERIC id is simultaneously a valid deployment id and a
	// valid bare revision, because hex digits include 0-9. The id must win:
	// resolving it as a revision sends the caller to a different deployment
	// than the one they named. Fixtures use "000...001" constantly and
	// gen_random_uuid can produce one, so this is reachable, not theoretical.
	{"all-numeric 32-char id is an id, not revision 1", "00000000000000000000000000000001", 0, false},
	{"all-numeric dashed uuid is an id", "00000000-0000-0000-0000-000000000001", 0, false},
	{"hyphenated uuid", "8f14e45f-ceea-467a-9c8e-9b0e21c6d5a1", 0, false},
	{"uuid starting with v-like hex", "deadbeefcafe4000a000000000000000", 0, false},

	// Non-positive and malformed input is rejected at the parser rather than
	// reaching the store: revisions are 1-based and 0 is the unassigned
	// sentinel, so "v0" is never a real handle.
	{"zero is the unassigned sentinel", "v0", 0, false},
	{"bare zero", "0", 0, false},
	{"negative", "v-1", 0, false},
	{"bare negative", "-1", 0, false},
	{"empty", "", 0, false},
	{"whitespace only", "   ", 0, false},
	{"v alone", "v", 0, false},
	{"non-numeric suffix", "v42x", 0, false},
	{"float", "v4.2", 0, false},
	{"leading plus", "+42", 0, false},
}

func TestParseDeploymentRevisionRef(t *testing.T) {
	for _, c := range revisionRefCases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := ParseDeploymentRevisionRef(c.in)
			if ok != c.wantOK {
				t.Fatalf("ParseDeploymentRevisionRef(%q) ok = %v, want %v", c.in, ok, c.wantOK)
			}
			if got != c.want {
				t.Errorf("ParseDeploymentRevisionRef(%q) = %d, want %d", c.in, got, c.want)
			}
		})
	}
}

// adr: 198
func TestRenderRevision(t *testing.T) {
	for _, c := range []struct {
		in   int
		want string
	}{
		{42, "v42"},
		{1, "v1"},
		// Non-positive renders empty so callers fall back to the deployment
		// id. Rendering "v0" would print a handle the customer could type
		// back in, and which would then fail to resolve.
		{0, ""},
		{-1, ""},
	} {
		if got := renderRevision(c.in); got != c.want {
			t.Errorf("renderRevision(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestParseDeploymentRevisionRef_RoundTripsRenderRevision pins that anything
// the product PRINTS as a revision is something it can READ back. A customer
// copying `v42` out of `gregale traffic status` into `gregale rollback --to`
// is the whole point of the handle.
//
// adr: 198
func TestParseDeploymentRevisionRef_RoundTripsRenderRevision(t *testing.T) {
	for _, revision := range []int{1, 2, 41, 42, 999, 100000} {
		rendered := renderRevision(revision)
		if rendered == "" {
			t.Fatalf("renderRevision(%d) returned empty for a positive revision", revision)
		}
		got, ok := ParseDeploymentRevisionRef(rendered)
		if !ok || got != revision {
			t.Errorf("round trip %d -> %q -> (%d, %v), want (%d, true)", revision, rendered, got, ok, revision)
		}
	}
}
