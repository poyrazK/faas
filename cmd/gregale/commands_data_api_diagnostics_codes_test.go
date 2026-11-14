package main_test

import (
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

// The runtime logs request codes and routes the diagnostics reader accepts;
// a code or route missing from the CLI turned every such record invalid
// (rls_required and every storage code did before).
func TestDataAPIDiagnosticCodesMatchRuntime(t *testing.T) {
	source, err := os.ReadFile("templates/data-api/logging.mjs")
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	start := strings.Index(text, "const codes = new Set([")
	end := strings.Index(text[start:], "])")
	if start < 0 || end < 0 {
		t.Fatal("the runtime code list moved")
	}
	quoted := regexp.MustCompile(`'([a-z_]+)'`)
	var runtime []string
	for _, match := range quoted.FindAllStringSubmatch(text[start:start+end], -1) {
		runtime = append(runtime, match[1])
	}
	cli := slices.Clone(*main.DataAPIDiagnosticCodes)
	slices.Sort(runtime)
	slices.Sort(cli)
	if !slices.Equal(runtime, cli) {
		t.Fatalf("runtime codes %v, CLI codes %v", runtime, cli)
	}
	routeStart := strings.Index(text, "function route(url) {")
	routeEnd := strings.Index(text[routeStart:], "\n}\n")
	var routes []string
	for _, match := range regexp.MustCompile(`return '([a-z]+)'`).FindAllStringSubmatch(text[routeStart:routeStart+routeEnd], -1) {
		routes = append(routes, match[1])
	}
	cliRoutes := slices.Clone(*main.DataAPIDiagnosticRoutes)
	slices.Sort(routes)
	slices.Sort(cliRoutes)
	if !slices.Equal(routes, cliRoutes) {
		t.Fatalf("runtime routes %v, CLI routes %v", routes, cliRoutes)
	}
}
