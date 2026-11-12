package main_test

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// TestCronExampleLogsFiredAtPerRun pins the cron starter's README promise
// (Hunt 33 H33-26): `gregale logs <slug> --follow` shows a fired_at line on
// every run. The handler used to only return fired_at in its response body,
// which a cron run never shows, so the logs stayed empty.
func TestCronExampleLogsFiredAtPerRun(t *testing.T) {
	readme, err := os.ReadFile("templates/cron-example/README.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(readme), "`fired_at` line") {
		t.Fatalf("cron-example README no longer promises a fired_at log line; update this test with it")
	}
	raw, err := os.ReadFile("templates/cron-example/handler.js")
	if err != nil {
		t.Fatal(err)
	}
	handler := string(raw)
	if !regexp.MustCompile(`const run = \{[^}]*\bfired_at:`).MatchString(handler) {
		t.Errorf("handler.js builds no run record with fired_at:\n%s", handler)
	}
	if !strings.Contains(handler, "console.log(JSON.stringify(run))") {
		t.Errorf("handler.js does not log the run record to stdout, so `gregale logs` shows no fired_at line:\n%s", handler)
	}
	if strings.Contains(handler, "gregale crons add") {
		t.Errorf("handler.js names `gregale crons add`; the documented command is `gregale cron create`")
	}
}
