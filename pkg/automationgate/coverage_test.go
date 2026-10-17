// adr: 1300
package automationgate_test

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/automationgate"
)

func TestCoveragePolicyParsing(t *testing.T) {
	for _, input := range []string{`{}`, `null`, `{"version":2,"required_cases":["a"]}`, `{"version":1,"required_cases":[]}`, `{"version":1,"required_cases":["a","a"]}`, `{"version":1,"required_cases":[" a"]}`, `{"version":1,"required_cases":["a"],"extra":true}`, `{"version":1,"version":1,"required_cases":["a"]}`, `{"version":1,"required_cases":["a"]} {}`, strings.Repeat(" ", 17000)} {
		if _, err := automationgate.ParseCoveragePolicy([]byte(input)); err == nil {
			t.Errorf("accepted %q", input)
		}
	}
	a, err := automationgate.ParseCoveragePolicy([]byte(`{"version":1,"required_cases":["b","a"]}`))
	if err != nil {
		t.Fatal(err)
	}
	b, err := automationgate.ParseCoveragePolicy([]byte("{\n\"required_cases\":[\"a\",\"b\"],\"version\":1}"))
	if err != nil {
		t.Fatal(err)
	}
	if a.Hash() != b.Hash() || len(a.Hash()) != 64 {
		t.Fatal("noncanonical hash")
	}
	b.RequiredCases[0] = "c"
	if a.Hash() == b.Hash() {
		t.Fatal("hash ignored coverage")
	}
}

func TestCoveragePolicyGate(t *testing.T) {
	for _, tc := range []struct {
		name     string
		required []string
		fail     bool
		code     int
		missing  string
	}{
		{"covered", []string{"steady", "recovered"}, false, 0, ""},
		{"omitted by both", []string{"critical"}, false, 7, "critical"},
		{"failure precedence", []string{"critical"}, true, 1, "critical"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, v := gateFixture()
			s.Coverage = &automationgate.CoveragePolicy{Version: 1, RequiredCases: tc.required}
			if tc.fail {
				v.Cases[0].Candidate.Result = "failed"
				v.Cases[0].Change = "regression"
				recount(&v)
			}
			r, err := automationgate.Evaluate(s, v)
			if err != nil {
				t.Fatal(err)
			}
			if r.ExitCode != tc.code || strings.Join(r.MissingRequiredCases, ",") != tc.missing {
				t.Fatalf("unexpected result: %+v", r)
			}
		})
	}
}
