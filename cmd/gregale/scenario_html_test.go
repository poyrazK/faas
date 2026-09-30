package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
