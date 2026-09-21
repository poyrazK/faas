package main

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// adr: 198
//
// revisionRefCases MUST stay byte-identical to the table in
// cmd/apid/deployment_ref_test.go. The CLI and the server parse `v42`
// independently (cmd/gregale must not import cmd/apid), so these two tables
// are what pin the parity the ADR claims. A change on one side that is not
// mirrored here fails there, and vice versa.
//
// If the two ever diverged, a customer would see one handle work in
// `gregale traffic set` (resolved client-side) and 404 in `gregale rollback`
// (resolved server-side) — the same string, two answers.
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

	{"uuid is not a revision", "8f14e45fceea467a9c8e9b0e21c6d5a1", 0, false},
	{"hyphenated uuid", "8f14e45f-ceea-467a-9c8e-9b0e21c6d5a1", 0, false},
	{"uuid starting with v-like hex", "deadbeefcafe4000a000000000000000", 0, false},

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

func TestParseRevisionRef(t *testing.T) {
	for _, c := range revisionRefCases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := parseRevisionRef(c.in)
			if ok != c.wantOK {
				t.Fatalf("parseRevisionRef(%q) ok = %v, want %v", c.in, ok, c.wantOK)
			}
			if got != c.want {
				t.Errorf("parseRevisionRef(%q) = %d, want %d", c.in, got, c.want)
			}
		})
	}
}

// adr: 198
func TestRenderRevisionCLI(t *testing.T) {
	for _, c := range []struct {
		in   int
		want string
	}{
		{42, "v42"},
		{1, "v1"},
		{0, ""},
		{-1, ""},
	} {
		if got := renderRevision(c.in); got != c.want {
			t.Errorf("renderRevision(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestDeploymentLabel pins the fallback that keeps pre-ADR-198 rows usable:
// a deployment with no revision prints its id, never "v0".
//
// adr: 198
func TestDeploymentLabel(t *testing.T) {
	withRevision := api.DeploymentResponse{ID: "8f14e45fceea467a9c8e9b0e21c6d5a1", Revision: 42}
	if got := deploymentLabel(withRevision); got != "v42" {
		t.Errorf("deploymentLabel(revision 42) = %q, want %q", got, "v42")
	}
	legacy := api.DeploymentResponse{ID: "8f14e45fceea467a9c8e9b0e21c6d5a1"}
	if got := deploymentLabel(legacy); got != legacy.ID {
		t.Errorf("deploymentLabel(no revision) = %q, want the id %q", got, legacy.ID)
	}
}

// TestResolveDeploymentRef_UUIDPassesThroughWithoutAPICall pins that a uuid
// costs no round trip — resolveDeploymentRef is called on the hot path of
// every `traffic set`, and a nil client here proves no API call was made.
//
// adr: 198
func TestResolveDeploymentRef_UUIDPassesThroughWithoutAPICall(t *testing.T) {
	const id = "8f14e45f-ceea-467a-9c8e-9b0e21c6d5a1"
	got, err := resolveDeploymentRef(t.Context(), nil, "", id)
	if err != nil {
		t.Fatalf("resolveDeploymentRef(uuid) error = %v, want nil", err)
	}
	if got != id {
		t.Errorf("resolveDeploymentRef(uuid) = %q, want it unchanged", got)
	}
}

// TestResolveDeploymentRef_RevisionRequiresApp pins the error a customer sees
// when they pass `v42` without --app. The endpoint carries no app context, so
// the revision is unresolvable and the message must say which flag is missing
// rather than failing somewhere deeper with a 404.
//
// adr: 198
func TestResolveDeploymentRef_RevisionRequiresApp(t *testing.T) {
	_, err := resolveDeploymentRef(t.Context(), nil, "", "v42")
	if err == nil {
		t.Fatal("resolveDeploymentRef(v42) with no app slug returned nil error")
	}
	if !strings.Contains(err.Error(), "--app") {
		t.Errorf("error %q does not name the missing --app flag", err)
	}
	if !strings.Contains(err.Error(), "v42") {
		t.Errorf("error %q does not echo the revision the customer typed", err)
	}
}
