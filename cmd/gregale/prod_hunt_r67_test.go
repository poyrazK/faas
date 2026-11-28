package main_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// Prod hunt r67: `app config SLUG --ram 7` suggested `gregale config SLUG
// --ram 7`, the local CLI settings command, which refuses the slug.
func TestAppConfigPointsAtAppFlags(t *testing.T) {
	r63Server(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		http.Error(w, "no", http.StatusTeapot)
	})
	code, out := r63Run(t, "app", "config", "shop", "--ram", "512")
	if code != 1 {
		t.Fatalf("exit = %d, want 1; out=%s", code, out)
	}
	if !strings.Contains(out, "Did you mean: gregale app shop --ram 512") {
		t.Fatalf("missing app-flag suggestion: %s", out)
	}
	if strings.Contains(out, "gregale config shop") {
		t.Fatalf("still suggests local config command: %s", out)
	}
}

// Prod hunt r69: a deleted database kept for its restore copies showed as
// `deleting` with no note in `postgres list` and only the raw
// restore_copies_remain code in `postgres get`.
func TestPostgresKeptForRestoreCopiesIsExplained(t *testing.T) {
	source := api.ManagedPostgresDatabase{ID: "2426a324-2384-4550-a6a8-686034d8a669", Name: "orders", State: "deleting", LastErrorCode: "restore_copies_remain"}
	copyDB := api.ManagedPostgresDatabase{ID: "3426a324-2384-4550-a6a8-686034d8a669", Name: "orders-copy", State: "ready", RestoreSourceDatabaseID: source.ID}
	r63Server(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/postgres/databases" {
			_ = json.NewEncoder(w).Encode(api.ManagedPostgresDatabaseList{Items: []api.ManagedPostgresDatabase{source, copyDB}})
			return
		}
		_ = json.NewEncoder(w).Encode(source)
	})
	code, out := r63Run(t, "postgres", "list")
	if code != 0 || !strings.Contains(out, "orders is deleted and kept for its restore copies: orders-copy") {
		t.Fatalf("list: exit=%d out=%s", code, out)
	}
	code, out = r63Run(t, "postgres", "get", "orders")
	if code != 0 || !strings.Contains(out, "note:             deleted and kept for its restore copies") {
		t.Fatalf("get: exit=%d out=%s", code, out)
	}
}

// Prod hunt r70: `gregale deployment <id>` printed only error and error_code
// for a failed deployment; the hint, why and fix the API returns were lost.
func TestDeploymentShowsTheFailureFix(t *testing.T) {
	const id = "d544a19931c24b03b9a3d46b67b796fe"
	r63Server(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/deployments/"+id {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(api.DeploymentResponse{
			ID: id, Status: "failed", Error: "snapshot prime failed", ErrorCode: "stage_snapshot_prepare_timeout",
			ErrorHint: "the snapshot_prepare stage exceeded the deadline",
			ErrorWhy:  "the deadline elapsed before the guest was ready",
			ErrorFix:  "• raise `startup_deadline_s` in gregale.yaml\n",
		})
	})
	code, out := r63Run(t, "deployment", id)
	if code != 0 {
		t.Fatalf("exit=%d out=%s", code, out)
	}
	for _, want := range []string{"the snapshot_prepare stage exceeded the deadline", "the deadline elapsed before the guest was ready", "raise `startup_deadline_s`"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
}

// Prod hunt r73: `projects environments bindtest` hinted "If bindtest is an
// app", and `environment list basic-hello` answered only "no such project
// resource".
func TestEnvironmentListNamesTheProject(t *testing.T) {
	r63Server(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"status":404,"code":"not_found","title":"Not found","detail":"no such project resource"}`))
	})
	code, out := r63Run(t, "projects", "environments", "bindtest")
	if code != 1 || !strings.Contains(out, "If bindtest is a project, the subcommand comes first") {
		t.Fatalf("hint: exit=%d out=%s", code, out)
	}
	code, out = r63Run(t, "environment", "list", "basic-hello")
	if code == 0 || !strings.Contains(out, `no project "basic-hello" in this account`) || !strings.Contains(out, "gregale project list") {
		t.Fatalf("list: exit=%d out=%s", code, out)
	}
}

// Prod hunt r73: `org members audit-1001-org` printed only "unknown
// subcommand", naming no verb.
func TestOrgMembersUnknownVerbListsTheVerbs(t *testing.T) {
	code, out := r63Run(t, "org", "members", "audit-1001-org")
	if code != 1 || !strings.Contains(out, `unknown subcommand "audit-1001-org"`) || !strings.Contains(out, "list") || !strings.Contains(out, "invite") {
		t.Fatalf("exit=%d out=%s", code, out)
	}
}

// Prod hunt r75: `redis get nope` answered "no such app".
func TestRedisGetNamesTheMissingCache(t *testing.T) {
	r63Server(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"status":404,"code":"not_found","title":"Not found","detail":"no such app"}`))
	})
	code, out := r63Run(t, "redis", "get", "nope")
	if code != 4 || !strings.Contains(out, `no Redis cache named "nope"`) || strings.Contains(out, "no such app") {
		t.Fatalf("exit=%d out=%s", code, out)
	}
}

// Prod hunt r75: `worker get nope` looked up an app named "get", `function
// get nope` printed the types usage and `queue get nope` printed usage
// without naming "get".
func TestUnknownVerbsAreNamed(t *testing.T) {
	r63Server(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		http.Error(w, "no", http.StatusTeapot)
	})
	for _, args := range [][]string{{"worker", "get", "nope"}, {"function", "get", "nope"}, {"queue", "get", "nope"}} {
		code, out := r63Run(t, args...)
		if code != 1 || !strings.Contains(out, `unknown subcommand "get"`) {
			t.Fatalf("%v: exit=%d out=%s", args, code, out)
		}
	}
}

// Prod hunt r76: these printed a bare usage line (or the usage twice) and
// never named the word that could not be used or where to go instead.
func TestAccountWideCommandsNameTheExtraWord(t *testing.T) {
	r63Server(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		http.Error(w, "no", http.StatusTeapot)
	})
	for _, tc := range []struct {
		args []string
		want []string
	}{
		{[]string{"postgres", "usage", "orders"}, []string{"postgres usage takes no database", "gregale postgres get orders"}},
		{[]string{"bucket", "usage", "shop"}, []string{`bucket usage takes no argument (got "shop")`, "gregale bucket list"}},
		{[]string{"usage", "shop"}, []string{`unknown usage subcommand "shop"`, "lists every app's usage"}},
		{[]string{"workflow", "get", "nope"}, []string{`"nope" is not a workflow run ID`, "gregale workflow list"}},
		{[]string{"app", "list", "--limit", "0"}, []string{"app list does not take --limit"}},
	} {
		code, out := r63Run(t, tc.args...)
		if code != 1 {
			t.Fatalf("%v: exit=%d out=%s", tc.args, code, out)
		}
		for _, want := range tc.want {
			if !strings.Contains(out, want) {
				t.Fatalf("%v: missing %q in:\n%s", tc.args, want, out)
			}
		}
		if strings.Contains(out, "usage: gregale billing usage") {
			t.Fatalf("%v: still prints the billing usage line: %s", tc.args, out)
		}
	}
}

// Prod hunt r73: deleted organizations are hidden from `org list` by the
// API; --all asks for them.
func TestOrgListAllAsksForDeletedOrgs(t *testing.T) {
	var queries []string
	r63Server(t, func(w http.ResponseWriter, r *http.Request) {
		queries = append(queries, r.URL.RawQuery)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"orgs":[{"slug":"audit-1001-org","plan":"free","status":"deleted_pending","name":"Audit"}]}`))
	})
	if code, out := r63Run(t, "org", "list"); code != 0 {
		t.Fatalf("list: exit=%d out=%s", code, out)
	}
	code, out := r63Run(t, "org", "list", "--all")
	if code != 0 || !strings.Contains(out, "audit-1001-org") {
		t.Fatalf("list --all: exit=%d out=%s", code, out)
	}
	if len(queries) != 2 || queries[0] != "" || queries[1] != "include_deleted=true" {
		t.Fatalf("queries = %q", queries)
	}
}

// Prod hunt r71: `logs --deployment` of a deployment whose snapshot prime
// failed showed only the build log; the boot output that explained the
// failure was in its instance's archive.
func TestFailedDeploymentLogsIncludeItsInstanceOutput(t *testing.T) {
	const dep = "2426a324-2384-4550-a6a8-686034d8a669"
	r63Server(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/deployments/"+dep:
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: dep, Status: "failed", Error: "snapshot prime failed", CreatedAt: time.Now().UTC().Format(time.RFC3339)})
		case r.URL.Path == "/v1/deployments/"+dep+"/logs":
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("event: log\ndata: {\"line\":\"build ok\"}\n\nevent: end\ndata: {}\n\n"))
		case r.URL.Path == "/v1/apps/shop/instances":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode([]api.InstanceResponse{
				{ID: "ins-prime", DeploymentID: dep, State: "failed"},
				{ID: "ins-other", DeploymentID: "other", State: "parked"},
			})
		case r.URL.Path == "/v1/apps/shop/logs" && r.URL.Query().Get("archive") == "1":
			if r.URL.Query().Get("instance") != "ins-prime" {
				t.Errorf("read another deployment's instance: %s", r.URL.RawQuery)
			}
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("event: log\ndata: Error: listen EADDRINUSE :8080\n\nevent: end\ndata: {}\n\n"))
		default:
			http.NotFound(w, r)
		}
	})
	code, out := r63Run(t, "logs", "shop", "--deployment", dep)
	if code != 0 {
		t.Fatalf("exit=%d out=%s", code, out)
	}
	for _, want := range []string{"build ok", "Retained output of instance ins-prime", "EADDRINUSE"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
}

// Prod hunt r68: `cron get nope` printed only its usage line, never that the
// ID was malformed or where cron IDs come from.
func TestCronGetNamesTheMalformedID(t *testing.T) {
	r63Server(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		http.Error(w, "no", http.StatusTeapot)
	})
	for _, verb := range []string{"get", "occurrences"} {
		code, out := r63Run(t, "cron", verb, "nope")
		if code != 1 || !strings.Contains(out, `"nope" is not a cron ID`) || !strings.Contains(out, "gregale cron list <app>") {
			t.Fatalf("cron %s nope: exit=%d out=%s", verb, code, out)
		}
	}
}
