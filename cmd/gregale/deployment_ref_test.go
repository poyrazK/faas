package main

import (
	"net/http"
	"net/http/httptest"
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
	// A 32-char ALL-NUMERIC id is simultaneously a valid deployment id and a
	// valid bare revision, because hex digits include 0-9. The id must win:
	// resolving it as a revision sends the caller to a different deployment
	// than the one they named. Fixtures use "000...001" constantly and
	// gen_random_uuid can produce one, so this is reachable, not theoretical.
	{"all-numeric 32-char id is an id, not revision 1", "00000000000000000000000000000001", 0, false},
	{"all-numeric dashed uuid is an id", "00000000-0000-0000-0000-000000000001", 0, false},
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

// TestValidDeploymentRef pins the ARGUMENT-VALIDATION gate that every
// deployment-addressing command now shares (ADR-198). Before this, each
// command checked deploymentIDPattern directly and rejected `v42` before it
// could ever be resolved.
//
// The uuid arm must stay exactly as strict as it was: a malformed id still
// has to fail locally with validation_failed rather than costing a 404
// round-trip (UX §3.3, "the first error is the right one"). The only new
// thing accepted is a form the product itself prints.
//
// adr: 198
func TestValidDeploymentRef(t *testing.T) {
	for _, c := range []struct {
		name string
		in   string
		want bool
	}{
		{"32-hex uuid", "8f14e45fceea467a9c8e9b0e21c6d5a1", true},
		{"dashed uuid", "8f14e45f-ceea-467a-9c8e-9b0e21c6d5a1", true},
		{"revision handle", "v42", true},
		{"bare revision", "42", true},

		// Still rejected locally — these are the cases the pattern gate
		// existed to catch, and widening it must not have cost them.
		{"too short", "8f14e45f", false},
		{"not hex", "zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz", false},
		{"empty", "", false},
		{"v0 is the unassigned sentinel", "v0", false},
		{"negative", "v-1", false},
		{"garbage", "not-a-deployment", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := validDeploymentRef(c.in); got != c.want {
				t.Errorf("validDeploymentRef(%q) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}

// TestResolveDeploymentArg_UUIDNeedsNoAppOrClient pins that widening these
// commands cost the existing invocation nothing. A uuid must short-circuit
// before any app resolution or API call — nil client and no app slug prove
// neither was touched.
//
// This matters because resolveDeploymentArg now runs on every one of the ~13
// deployment-addressing commands: if the uuid path consulted the linked
// project, every command would start failing outside a linked checkout.
//
// adr: 198
func TestResolveDeploymentArg_UUIDNeedsNoAppOrClient(t *testing.T) {
	const id = "8f14e45fceea467a9c8e9b0e21c6d5a1"
	got, err := resolveDeploymentArg(t.Context(), nil, "", id)
	if err != nil {
		t.Fatalf("resolveDeploymentArg(uuid) error = %v, want nil", err)
	}
	if got != id {
		t.Errorf("resolveDeploymentArg(uuid) = %q, want it unchanged", got)
	}
}

// TestResolveDeploymentArg_RevisionWithoutAppNamesTheFix pins the error a
// customer sees when they type a revision with no --app and no linked
// project. The link machinery's own message ("project has multiple or no
// workloads") describes ITS problem, not theirs — the actionable fix is to
// say which app the revision belongs to.
//
// adr: 198
func TestResolveDeploymentArg_RevisionWithoutAppNamesTheFix(t *testing.T) {
	// t.Chdir to a directory with no .gregale/ link so the context lookup
	// genuinely fails rather than picking up the repo's own state.
	t.Chdir(t.TempDir())
	_, err := resolveDeploymentArg(t.Context(), nil, "", "v42")
	if err == nil {
		t.Fatal("resolveDeploymentArg(v42) with no app and no link returned nil error")
	}
	for _, want := range []string{"v42", "--app", "gregale link"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

// TestCmdDeployment_ResolvesRevisionEndToEnd is the test that would have
// caught the gap this change closes: after ADR-198 shipped, `gregale
// deployment v42` still failed because each command validated
// deploymentIDPattern directly and rejected the handle the product had just
// taught the customer to use.
//
// It drives the real command through a fake API and asserts the two requests
// that matter: the deployments list used to resolve v42, and the drill-down
// fetched by the RESOLVED uuid rather than the literal "v42".
//
// adr: 198
func TestCmdDeployment_ResolvesRevisionEndToEnd(t *testing.T) {
	const wantID = "8f14e45fceea467a9c8e9b0e21c6d5a1"
	var listed, fetched string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/apps/my-api/deployments":
			listed = r.URL.Path
			writeJSONTest(w, api.DeploymentListResponse{Items: []api.DeploymentResponse{
				{ID: "1b6453892473a467d07372d45eb05abc", Revision: 41, Status: statusLive},
				{ID: wantID, Revision: 42, Status: statusLive},
			}})
		case strings.HasPrefix(r.URL.Path, "/v1/deployments/"):
			fetched = strings.TrimPrefix(r.URL.Path, "/v1/deployments/")
			writeJSONTest(w, api.DeploymentResponse{ID: wantID, Revision: 42, Status: statusLive})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_test_x")

	_, restore := captureStdout(t)
	defer restore()
	if code := cmdDeployment([]string{"v42", "--app", "my-api"}); code != 0 {
		t.Fatalf("gregale deployment v42 --app my-api exit = %d, want 0", code)
	}
	if listed == "" {
		t.Error("revision was not resolved through the app's deployment list")
	}
	if fetched != wantID {
		t.Errorf("drill-down fetched %q, want the resolved uuid %q (the literal handle must not reach the API)", fetched, wantID)
	}
}

// TestCmdDeployment_UUIDSkipsResolution is the other half: passing a uuid
// must not consult the deployments list at all. A regression here would add
// an API round-trip to every existing invocation of every one of these
// commands.
//
// adr: 198
func TestCmdDeployment_UUIDSkipsResolution(t *testing.T) {
	const id = "8f14e45fceea467a9c8e9b0e21c6d5a1"
	var listCalls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/apps/my-api/deployments" {
			listCalls++
		}
		writeJSONTest(w, api.DeploymentResponse{ID: id, Revision: 42, Status: statusLive})
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_test_x")

	_, restore := captureStdout(t)
	defer restore()
	if code := cmdDeployment([]string{id, "--app", "my-api"}); code != 0 {
		t.Fatalf("gregale deployment <uuid> exit = %d, want 0", code)
	}
	if listCalls != 0 {
		t.Errorf("uuid path made %d deployment-list call(s); it must short-circuit", listCalls)
	}
}
