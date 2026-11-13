package main_test

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

// ChronoLake report: `cron run --wait` printed a resume_command spelled with
// the replaced `crons fire-now` path. Commands the CLI prints name the tree.
func TestCronRunCommandUsesTreeSpelling(t *testing.T) {
	got := main.CronRunCommand("fire-now", "req-1")
	if got != "gregale cron run get req-1" {
		t.Fatalf("fire-now resume command = %q, want %q", got, "gregale cron run get req-1")
	}
	if run := main.CronRunCommand("run", "cron-1"); strings.Contains(run, "crons ") || !strings.HasSuffix(run, " cron-1") {
		t.Fatalf("run command = %q, want the tree spelling ending with the id", run)
	}
}

// ChronoLake report: `gregale bucket --help` linked
// https://gregale.dev/docs/object-storage, a page the docs site does not
// publish (404). Help never links a route docs/customer-pages.json marks
// unpublished; it falls back to the CLI reference.
func TestHelpDocsLinksSkipUnpublishedPages(t *testing.T) {
	raw, err := os.ReadFile("../../docs/customer-pages.json")
	if err != nil {
		t.Fatal(err)
	}
	var catalog struct {
		BaseURL string `json:"base_url"`
		Routes  []struct {
			Path        string `json:"path"`
			Unpublished string `json:"unpublished"`
		} `json:"routes"`
	}
	if err := json.Unmarshal(raw, &catalog); err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, route := range catalog.Routes {
		if route.Unpublished == "" || route.Path == "" {
			continue
		}
		checked++
		if got := main.DocsURLForTopic(route.Path); got == catalog.BaseURL+"/"+route.Path {
			t.Errorf("help for %q links unpublished page %s (%s)", route.Path, got, route.Unpublished)
		}
	}
	if checked == 0 {
		t.Fatal("customer-pages.json lists no unpublished routes; the check is vacuous")
	}
}

// ChronoLake report: `metric requests SLUG --range 1h` failed with unknown
// flag although `gregale metric --help` names --range for every view.
func TestMetricRequestsAcceptsRange(t *testing.T) {
	enableEveryCLIFeature(t)
	// Parsing succeeds, so the command goes on to need a session.
	code, _, stderr := runCLI(t, "metric", "requests", "--range", "1h", "my-app")
	if strings.Contains(stderr, "unknown flag") || !strings.Contains(stderr, "Not logged in") {
		t.Fatalf("metric requests --range = %d, stderr %q; want it parsed", code, stderr)
	}
}
