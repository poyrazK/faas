package labels_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/labels"
)

func TestParseCanonical(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"", ""},
		{"   ", ""},
		{"team=billing", "team=billing"},
		{"team==billing", "team=billing"},
		{" team = billing ", "team=billing"},
		{"team!=billing", "team!=billing"},
		{"team=", "team="},
		{"canary", "canary"},
		{"!canary", "!canary"},
		{"! canary", "!canary"},
		{"tier in (web,api)", "tier in (api,web)"},
		{"tier in(web, api, web)", "tier in (api,web)"},
		{"tier notin (web)", "tier notin (web)"},
		{"tier in (,web)", "tier in (,web)"},
		{"tier in ()", "tier in ()"},
		{"tier notin (,)", "tier notin ()"},
		{"app.kubernetes.io/name=shop,gregale.dev/imported-from", "app.kubernetes.io/name=shop,gregale.dev/imported-from"},
		{"team=billing,tier in (web,api),!canary,env!=dev", "team=billing,tier in (api,web),!canary,env!=dev"},
		{"in", "in"},
		{"in in (in)", "in in (in)"},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			sel, err := labels.Parse(tc.in)
			if err != nil {
				t.Fatalf("Parse(%q): %v", tc.in, err)
			}
			if got := sel.String(); got != tc.want {
				t.Errorf("String() = %q, want %q", got, tc.want)
			}
			again, err := labels.Parse(sel.String())
			if err != nil || again.String() != tc.want {
				t.Errorf("canonical form does not round-trip: %q, %v", again.String(), err)
			}
		})
	}
}

func TestParseErrors(t *testing.T) {
	tests := []struct {
		in          string
		requirement string
		message     string
	}{
		{"team=billing,", "", "expected a label key"},
		{",team", "", "expected a label key"},
		{"team=billing,,tier", "", "expected a label key"},
		{"tier in (web", "tier in (web", "expected ',' or ')'"},
		{"tier in web", "tier in web", "expected '('"},
		{"tier like web", "tier like web", `unknown operator "like"`},
		{"team=bill ing", "team=bill ing", "expected ',' or the end"},
		{"team=a,bad key=x", "bad key=x", `unknown operator "key"`},
		{"team=billing,tier=we b", "tier=we b", "expected ',' or the end"},
		{"-team=x", "-team=x", "starting and ending"},
		{"team=-x", "team=-x", "the value must be"},
		{"tier in (web,-x)", "tier in (web,-x)", "the value must be"},
		{"!team=x", "!team=x", "expected ',' or the end"},
		{"!", "!", "expected a label key"},
		{"team)", "team)", "expected an operator"},
		{"=x", "=x", "expected a label key"},
		{"Example.com/x=y", "Example.com/x=y", "lowercase DNS subdomain"},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			_, err := labels.Parse(tc.in)
			var le *labels.Error
			if !errors.As(err, &le) {
				t.Fatalf("Parse(%q) err = %v, want *labels.Error", tc.in, err)
			}
			if le.Code != labels.CodeInvalidSelector {
				t.Errorf("code = %q", le.Code)
			}
			if le.Key != tc.requirement {
				t.Errorf("requirement = %q, want %q", le.Key, tc.requirement)
			}
			if !strings.Contains(le.Message, tc.message) {
				t.Errorf("message = %q, want it to contain %q", le.Message, tc.message)
			}
		})
	}
}

func TestParseRefusesLongSelector(t *testing.T) {
	long := "team in (" + strings.Repeat("a,", api.LabelSelectorMaxBytes/2) + "a)"
	_, err := labels.Parse(long)
	var le *labels.Error
	if !errors.As(err, &le) || le.Limit != api.LabelSelectorMaxBytes || le.Observed != len(long) {
		t.Fatalf("err = %v", err)
	}
}

func TestSelectorMatches(t *testing.T) {
	billingWeb := map[string]string{"team": "billing", "tier": "web"}
	billingCanary := map[string]string{"team": "billing", "tier": "api", "canary": ""}
	payments := map[string]string{"team": "payments"}
	tests := []struct {
		selector string
		matches  []map[string]string
		misses   []map[string]string
	}{
		{"", []map[string]string{billingWeb, payments, nil}, nil},
		{"team=billing", []map[string]string{billingWeb, billingCanary}, []map[string]string{payments, nil}},
		{"team!=billing", []map[string]string{payments, nil}, []map[string]string{billingWeb}},
		{"tier", []map[string]string{billingWeb, billingCanary}, []map[string]string{payments}},
		{"!tier", []map[string]string{payments, nil}, []map[string]string{billingWeb}},
		{"tier in (web,api)", []map[string]string{billingWeb, billingCanary}, []map[string]string{payments}},
		{"tier notin (web)", []map[string]string{billingCanary, payments, nil}, []map[string]string{billingWeb}},
		{"canary=", []map[string]string{billingCanary}, []map[string]string{billingWeb}},
		{"team=billing,!canary", []map[string]string{billingWeb}, []map[string]string{billingCanary, payments}},
		{"team=billing,tier in (web,api),!canary", []map[string]string{billingWeb}, []map[string]string{billingCanary}},
		{"team!=billing,team!=payments", []map[string]string{nil}, []map[string]string{billingWeb, payments}},
	}
	for _, tc := range tests {
		t.Run(tc.selector, func(t *testing.T) {
			sel, err := labels.Parse(tc.selector)
			if err != nil {
				t.Fatal(err)
			}
			for _, l := range tc.matches {
				if !sel.Matches(l) {
					t.Errorf("%q should match %v", tc.selector, l)
				}
			}
			for _, l := range tc.misses {
				if sel.Matches(l) {
					t.Errorf("%q should not match %v", tc.selector, l)
				}
			}
		})
	}
}

func TestZeroSelectorMatchesEverything(t *testing.T) {
	var sel labels.Selector
	if !sel.Empty() || !sel.Matches(map[string]string{"a": "b"}) || !sel.Matches(nil) {
		t.Fatal("the zero Selector must match every resource")
	}
}

func FuzzParse(f *testing.F) {
	for _, seed := range []string{"team=billing", "tier in (web,api),!canary", "a!=b", "x notin (,y)", "((", "!=", "a=b,"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, in string) {
		sel, err := labels.Parse(in)
		if err != nil {
			var le *labels.Error
			if !errors.As(err, &le) || le.Code != labels.CodeInvalidSelector {
				t.Fatalf("Parse(%q) err = %v, want an invalid_label_selector *labels.Error", in, err)
			}
			return
		}
		again, err := labels.Parse(sel.String())
		if err != nil {
			t.Fatalf("canonical %q of %q does not parse: %v", sel.String(), in, err)
		}
		if again.String() != sel.String() {
			t.Fatalf("canonical form is not stable: %q then %q", sel.String(), again.String())
		}
		_ = sel.Matches(map[string]string{"team": "billing"})
	})
}
