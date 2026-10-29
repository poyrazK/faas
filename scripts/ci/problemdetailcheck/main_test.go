package main

import (
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

func findings(t *testing.T, rel, body string) []string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, rel, body, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	return check(fset, f, rel)
}

func TestCheckFlagsErrorTextInServerProblems(t *testing.T) {
	const head = `package x

import (
	"fmt"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
)

func f(err, storeErr error, raw string) {
`
	for _, tc := range []struct {
		name, stmt string
		flagged    bool
	}{
		{"internal err.Error", `_ = api.ErrInternal(err.Error())`, true},
		{"capacity sprintf", `_ = api.ErrCapacity(fmt.Sprintf("load: %v", err))`, true},
		{"500 named error", `_ = api.NewProblem(http.StatusInternalServerError, "c", "t", "x: "+storeErr.Error())`, true},
		{"502 literal status", `_ = api.NewProblem(502, "c", "t", fmt.Sprint(storeErr))`, true},
		{"second detail arg", `_ = api.ErrDoctorUnavailable("d", err.Error())`, true},
		{"fixed detail", `_ = api.ErrInternal(api.DetailServerFailure)`, false},
		{"4xx echoes validation", `_ = api.NewProblem(http.StatusBadRequest, "c", "t", err.Error())`, false},
		{"5xx without error", `_ = api.NewProblem(http.StatusServiceUnavailable, "c", "t", raw)`, false},
		{"domain in first arg", `_ = api.ErrDoctorUnavailable(err.Error(), "a probe could not run")`, false},
		{"allowed", "// problemdetail:allow fixture\n_ = api.ErrInternal(err.Error())", false},
	} {
		got := findings(t, "pkg/x/x.go", head+tc.stmt+"\n}\n")
		if (len(got) > 0) != tc.flagged {
			t.Errorf("%s: findings %v, want flagged=%v", tc.name, got, tc.flagged)
		}
	}
}

func TestCheckFollowsTheImportName(t *testing.T) {
	got := findings(t, "pkg/x/x.go", `package x

import papi "github.com/onebox-faas/faas/pkg/api"

func f(err error) { _ = papi.ErrInternal(err.Error()) }
`)
	if len(got) != 1 || !strings.HasPrefix(got[0], "pkg/x/x.go:5: ErrInternal") {
		t.Fatalf("findings = %v", got)
	}
	if got := findings(t, "pkg/x/x.go", "package x\n\nfunc f(err error) { _ = ErrInternal(err.Error()) }\n"); len(got) != 0 {
		t.Fatalf("a file that does not import pkg/api: %v", got)
	}
}

func TestCheckCoversPkgAPIItself(t *testing.T) {
	got := findings(t, "pkg/api/x.go", `package api

import "net/http"

func g(err error) *Problem { return NewProblem(http.StatusBadGateway, "c", "t", err.Error()) }
`)
	if len(got) != 1 {
		t.Fatalf("findings = %v", got)
	}
}

// The tree stays clean: a new 5xx problem carrying error text fails here
// as well as in the ci-light step.
func TestTreeHasNoServerProblemWithErrorText(t *testing.T) {
	got, err := scan("../../..", []string{"pkg", "cmd"})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range got {
		t.Error(f)
	}
}
