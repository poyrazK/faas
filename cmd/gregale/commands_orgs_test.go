// commands_orgs_test.go — IAM-6 / ADR-061 / issue #190 smoke tests
// for the `gregale orgs <subcommand>` family + the standalone
// `gregale invitations <peek|accept>` surface. Mirrors
// commands_admin_test.go shape: arg-validation exits 1, auth-gate
// exits 1, happy path hits the right route with the right body,
// closed-vocab typos fail locally before the round-trip.

package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// --- dispatcher / auth gate ------------------------------------------------

func TestOrgs_NoSubcommandExitsOne(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_TOKEN", "test")
	t.Setenv("FAAS_API", "http://unused")
	if code := cmdOrgs(nil); code != 1 {
		t.Errorf("orgs (no sub) exit = %d, want 1", code)
	}
}

func TestOrgs_UnknownSubcommandExitsOne(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_TOKEN", "test")
	t.Setenv("FAAS_API", "http://unused")
	if code := cmdOrgs([]string{"frobnicate"}); code != 1 {
		t.Errorf("orgs frobnicate exit = %d, want 1", code)
	}
}

func TestOrgs_NoTokenExitsOne(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_TOKEN", "")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Errorf("no network call expected without a token")
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)

	for _, args := range [][]string{
		{"list"},
		{"create", "--slug", "acme", "--name", "ACME Co"},
		{"info", "acme"},
		{"rm", "-q", "acme"},
		{"members", "list", "acme"},
		{"members", "invite", "--org", "acme", "--email", "x@y.z"},
		{"members", "change-role", "--org", "acme", "--user", "u-1", "--role", "admin"},
		{"members", "rm", "--org", "acme", "--user", "u-1"},
		{"invitations", "list", "--org", "acme"},
		{"invitations", "revoke", "--org", "acme", "--invitation", "inv-1"},
		{"transfer-ownership", "--org", "acme", "--to", "u-2"},
		{"seat-usage", "--org", "acme"},
	} {
		// errAuth returns exit code 2 (auth), not 1 (user error).
		if code := cmdOrgs(args); code != 2 {
			t.Errorf("orgs %v exit = %d, want 2 (auth)", args, code)
		}
	}
}

// --- org CRUD -------------------------------------------------------------

func TestOrgs_Ls_HappyPath(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_TOKEN", "test")

	var sawMethod, sawPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawMethod = r.Method
		sawPath = r.URL.Path
		_ = json.NewEncoder(w).Encode(api.OrgListResponse{Orgs: []api.OrgResponse{
			{Slug: "acme", Name: "ACME Co", Plan: "pro", Status: "active", Personal: false},
			{Slug: "personal", Name: "Personal", Plan: "free", Status: "active", Personal: true},
		}})
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)

	for _, subcommand := range []string{"list", "ls"} {
		sawMethod, sawPath = "", ""
		if code := cmdOrgs([]string{subcommand}); code != 0 {
			t.Fatalf("orgs %s exit = %d, want 0", subcommand, code)
		}
		if sawMethod != "GET" || sawPath != "/v1/orgs" {
			t.Errorf("orgs %s route = %s %s, want GET /v1/orgs", subcommand, sawMethod, sawPath)
		}
	}
}

func TestOrgs_Create_HappyPath(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_TOKEN", "test")

	var sawMethod, sawPath string
	var sawBody api.CreateOrgRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawMethod = r.Method
		sawPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&sawBody)
		_ = json.NewEncoder(w).Encode(api.OrgResponse{Slug: "acme", Name: "ACME Co", Plan: "free"})
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)

	if code := cmdOrgs([]string{"create", "--slug", "acme", "--name", "ACME Co"}); code != 0 {
		t.Fatalf("orgs create exit = %d, want 0", code)
	}
	if sawMethod != "POST" || sawPath != "/v1/orgs" {
		t.Errorf("route = %s %s, want POST /v1/orgs", sawMethod, sawPath)
	}
	if sawBody.Slug != "acme" || sawBody.Name != "ACME Co" {
		t.Errorf("body = %+v, want {acme ACME Co}", sawBody)
	}
}

func TestOrgs_Create_MissingNameExitsOne(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_TOKEN", "test")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Errorf("no network call expected on missing --name")
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)

	if code := cmdOrgs([]string{"create", "--slug", "acme"}); code != 1 {
		t.Errorf("orgs create (no --name) exit = %d, want 1", code)
	}
}

func TestOrgs_Info_HappyPath(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_TOKEN", "test")

	var sawMethod, sawPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawMethod = r.Method
		sawPath = r.URL.Path
		_ = json.NewEncoder(w).Encode(api.OrgResponse{Slug: "acme", Name: "ACME Co", Plan: "pro", Status: "active"})
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)

	if code := cmdOrgs([]string{"info", "acme"}); code != 0 {
		t.Fatalf("orgs info exit = %d, want 0", code)
	}
	if sawMethod != "GET" || sawPath != "/v1/orgs/acme" {
		t.Errorf("route = %s %s, want GET /v1/orgs/acme", sawMethod, sawPath)
	}
}

func TestOrgs_Rm_HappyPath(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_TOKEN", "test")

	var sawMethod, sawPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawMethod = r.Method
		sawPath = r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)

	if code := cmdOrgs([]string{"rm", "-q", "acme"}); code != 0 {
		t.Fatalf("orgs rm -q exit = %d, want 0", code)
	}
	if sawMethod != "DELETE" || sawPath != "/v1/orgs/acme" {
		t.Errorf("route = %s %s, want DELETE /v1/orgs/acme", sawMethod, sawPath)
	}
}

// --- members --------------------------------------------------------------

func TestOrgs_MembersLs_HappyPath(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_TOKEN", "test")

	var sawPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawPath = r.URL.Path
		_ = json.NewEncoder(w).Encode(api.MemberListResponse{Members: []api.OrgMemberResponse{
			{AccountID: "u-1", Email: "a@example.com", Role: "owner"},
			{AccountID: "u-2", Email: "b@example.com", Role: "admin"},
		}})
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)

	if code := cmdOrgs([]string{"members", "list", "acme"}); code != 0 {
		t.Fatalf("orgs members list exit = %d, want 0", code)
	}
	if sawPath != "/v1/orgs/acme/members" {
		t.Errorf("path = %s, want /v1/orgs/acme/members", sawPath)
	}
}

func TestOrgs_MembersInvite_HappyPath(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_TOKEN", "test")

	var sawBody api.InviteMemberRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&sawBody)
		_ = json.NewEncoder(w).Encode(api.InvitationWithTokenResponse{
			OrgInvitationResponse: api.OrgInvitationResponse{ID: "inv-1", Email: "x@y.z", Role: "developer"},
			Token:                 "tok-once",
		})
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)

	if code := cmdOrgs([]string{"members", "invite", "--org", "acme", "--email", "x@y.z"}); code != 0 {
		t.Fatalf("orgs members invite exit = %d, want 0", code)
	}
	if sawBody.Email != "x@y.z" || sawBody.Role != "developer" {
		t.Errorf("body = %+v, want {x@y.z developer}", sawBody)
	}
}

func TestOrgs_MembersInvite_OwnerRoleRejected(t *testing.T) {
	// Closed-set guard: --role=owner must fail locally (the API would
	// reject it anyway, but a typo "owne" should also fail locally).
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_TOKEN", "test")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Errorf("no network call expected on --role=owner")
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)

	for _, role := range []string{"owner", "owne", "OWNER"} {
		if code := cmdOrgs([]string{"members", "invite", "--org", "acme", "--email", "x@y.z", "--role", role}); code != 1 {
			t.Errorf("orgs members invite --role=%s exit = %d, want 1", role, code)
		}
	}
}

func TestOrgs_MembersChangeRole_HappyPath(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_TOKEN", "test")

	var sawMethod, sawPath string
	var sawBody api.ChangeMemberRoleRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawMethod = r.Method
		sawPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&sawBody)
		_ = json.NewEncoder(w).Encode(api.OrgMemberResponse{AccountID: "u-1", Email: "x@y.z", Role: "admin"})
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)

	if code := cmdOrgs([]string{"members", "change-role", "--org", "acme", "--user", "u-1", "--role", "admin"}); code != 0 {
		t.Fatalf("orgs members change-role exit = %d, want 0", code)
	}
	if sawMethod != "PATCH" || sawPath != "/v1/orgs/acme/members/u-1" {
		t.Errorf("route = %s %s, want PATCH /v1/orgs/acme/members/u-1", sawMethod, sawPath)
	}
	if sawBody.Role != "admin" {
		t.Errorf("body role = %q, want admin", sawBody.Role)
	}
}

func TestOrgs_MembersChangeRole_OwnerRoleRejected(t *testing.T) {
	// PATCH cannot promote a member to owner (only transfer-ownership can).
	// The CLI must reject --role=owner locally rather than round-trip.
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_TOKEN", "test")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Errorf("no network call expected on --role=owner")
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)

	for _, role := range []string{"owner", "owne", "OWNER"} {
		if code := cmdOrgs([]string{"members", "change-role", "--org", "acme", "--user", "u-1", "--role", role}); code != 1 {
			t.Errorf("orgs members change-role --role=%s exit = %d, want 1", role, code)
		}
	}
}

func TestOrgs_MembersRm_HappyPath(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_TOKEN", "test")

	var sawMethod, sawPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawMethod = r.Method
		sawPath = r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)

	if code := cmdOrgs([]string{"members", "rm", "--org", "acme", "--user", "u-1"}); code != 0 {
		t.Fatalf("orgs members rm exit = %d, want 0", code)
	}
	if sawMethod != "DELETE" || sawPath != "/v1/orgs/acme/members/u-1" {
		t.Errorf("route = %s %s, want DELETE /v1/orgs/acme/members/u-1", sawMethod, sawPath)
	}
}

// --- invitations + ownership + seats --------------------------------------

func TestOrgs_InvitationsLs_HappyPath(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_TOKEN", "test")

	var sawPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawPath = r.URL.Path
		_ = json.NewEncoder(w).Encode(api.InvitationListResponse{Invitations: []api.OrgInvitationResponse{
			{ID: "inv-1", Email: "x@y.z", Role: "developer", Status: "pending"},
		}})
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)

	if code := cmdOrgs([]string{"invitations", "list", "--org", "acme"}); code != 0 {
		t.Fatalf("orgs invitations list exit = %d, want 0", code)
	}
	if sawPath != "/v1/orgs/acme/invitations" {
		t.Errorf("path = %s, want /v1/orgs/acme/invitations", sawPath)
	}
}

func TestOrgs_InvitationsRevoke_HappyPath(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_TOKEN", "test")

	var sawMethod, sawPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawMethod = r.Method
		sawPath = r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)

	if code := cmdOrgs([]string{"invitations", "revoke", "--org", "acme", "--invitation", "inv-1"}); code != 0 {
		t.Fatalf("orgs invitations revoke exit = %d, want 0", code)
	}
	if sawMethod != "DELETE" || sawPath != "/v1/orgs/acme/invitations/inv-1" {
		t.Errorf("route = %s %s, want DELETE /v1/orgs/acme/invitations/inv-1", sawMethod, sawPath)
	}
}

func TestOrgs_TransferOwnership_HappyPath(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_TOKEN", "test")

	var sawMethod, sawPath string
	var sawBody api.TransferOwnershipRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawMethod = r.Method
		sawPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&sawBody)
		_ = json.NewEncoder(w).Encode(api.OrgResponse{Slug: "acme", Plan: "pro"})
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)

	if code := cmdOrgs([]string{"transfer-ownership", "--org", "acme", "--to", "u-2"}); code != 0 {
		t.Fatalf("orgs transfer-ownership exit = %d, want 0", code)
	}
	if sawMethod != "POST" || sawPath != "/v1/orgs/acme/transfer_ownership" {
		t.Errorf("route = %s %s, want POST /v1/orgs/acme/transfer_ownership", sawMethod, sawPath)
	}
	if sawBody.NewOwnerAccountID != "u-2" {
		t.Errorf("body new_owner_account_id = %q, want u-2", sawBody.NewOwnerAccountID)
	}
}

func TestOrgs_SeatUsage_HappyPath(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_TOKEN", "test")

	var sawPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawPath = r.URL.Path
		_ = json.NewEncoder(w).Encode(api.SeatUsageResponse{Plan: "pro", Used: 3, Limit: 10})
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)

	if code := cmdOrgs([]string{"seat-usage", "--org", "acme"}); code != 0 {
		t.Fatalf("orgs seat-usage exit = %d, want 0", code)
	}
	if sawPath != "/v1/orgs/acme/seat_usage" {
		t.Errorf("path = %s, want /v1/orgs/acme/seat_usage", sawPath)
	}
}

// --- standalone invitations ------------------------------------------------

func TestInvitations_NoSubcommandExitsOne(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_TOKEN", "test")
	t.Setenv("FAAS_API", "http://unused")
	if code := cmdInvitations(nil); code != 1 {
		t.Errorf("invitations (no sub) exit = %d, want 1", code)
	}
}

func TestInvitations_Peek_HappyPath(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_TOKEN", "test")

	var sawPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawPath = r.URL.Path
		_ = json.NewEncoder(w).Encode(api.OrgInvitationResponse{ID: "inv-1", OrgSlug: "acme", Email: "x@y.z", Role: "developer", Status: "pending"})
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)

	if code := cmdInvitations([]string{"peek", "tok-once"}); code != 0 {
		t.Fatalf("invitations peek exit = %d, want 0", code)
	}
	if sawPath != "/v1/invitations/tok-once" {
		t.Errorf("path = %s, want /v1/invitations/tok-once", sawPath)
	}
}

func TestInvitations_Accept_HappyPath(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_TOKEN", "test")

	var sawMethod, sawPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawMethod = r.Method
		sawPath = r.URL.Path
		_ = json.NewEncoder(w).Encode(api.OrgMemberResponse{AccountID: "u-1", Email: "x@y.z", Role: "developer"})
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)

	if code := cmdInvitations([]string{"accept", "tok-once"}); code != 0 {
		t.Fatalf("invitations accept exit = %d, want 0", code)
	}
	if sawMethod != "POST" || sawPath != "/v1/invitations/tok-once/accept" {
		t.Errorf("route = %s %s, want POST /v1/invitations/tok-once/accept", sawMethod, sawPath)
	}
}

func TestInvitations_Accept_NoTokenExitsOne(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_TOKEN", "")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Errorf("no network call expected without a token")
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)

	if code := cmdInvitations([]string{"accept", "tok-once"}); code != 2 {
		t.Errorf("invitations accept (no token) exit = %d, want 2 (auth)", code)
	}
}
