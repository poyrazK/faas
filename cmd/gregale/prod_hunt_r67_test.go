package main_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

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
