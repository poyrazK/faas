package main_test

import (
	"net/http"
	"strings"
	"testing"
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
