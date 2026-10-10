package main

import (
	"github.com/onebox-faas/faas/pkg/chaos"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRenderTestHTMLReportEscapesDataAndEmbedsStaticStyles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.html")
	const source = `<!doctype html><html><head><style>{{styles}}</style></head><body><p>{{.Value}}</p></body></html>`
	data := struct{ Value string }{Value: `<script>alert(1)</script>`}

	if err := renderTestHTML(path, source, data); err != nil {
		t.Fatalf("renderTestHTML() error = %v", err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read rendered report: %v", err)
	}
	rendered := string(body)
	if !strings.Contains(rendered, "<style>"+testHTMLStyles+"</style>") {
		t.Fatal("rendered report does not contain the static stylesheet")
	}
	if !strings.Contains(rendered, "&lt;script&gt;alert(1)&lt;/script&gt;") {
		t.Fatal("report data was not HTML-escaped")
	}
	if strings.Contains(rendered, "<script>alert(1)</script>") {
		t.Fatal("report data was emitted as executable HTML")
	}
}

func TestHTMLReportShowsTCPFaultParameters(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.html")
	receipt := testRunReceipt{Scenario: "tcp-example", Status: "passed", Chaos: &testChaosEvidence{
		ExpiresAt: time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC), RulesInstalled: 1,
		Rules: []chaos.Rule{{To: "cache", Kind: chaos.KindTCPBandwidth, Port: 6379, Direction: chaos.DirectionDownstream, RateKiBPerSecond: 64, Percent: 100}},
	}}
	if err := writeTestHTMLReport(path, "", []testRunReceipt{receipt}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"Installed chaos rules", "tcp_bandwidth", "6379", "downstream", "64", "2026-10-07"} {
		if !strings.Contains(string(data), value) {
			t.Fatalf("report omitted %q", value)
		}
	}
}
