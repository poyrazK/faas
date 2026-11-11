package gregalemanifest_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/eventfilter"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
)

// Events hunt #9: an event_triggers filter over the size or depth cap is
// refused in both manifest formats, as a bound error the apply paths map to a
// limit problem.
func TestEventTriggerFilterCaps(t *testing.T) {
	large := `{"data":{"id":{"$eq":"` + strings.Repeat("x", api.EventSubscriptionFilterMaxBytes) + `"}}}`
	deep := strings.Repeat(`{"a":`, api.EventSubscriptionFilterMaxDepth) + `{"$eq":1}` + strings.Repeat("}", api.EventSubscriptionFilterMaxDepth)
	atDepth := strings.Repeat(`{"a":`, api.EventSubscriptionFilterMaxDepth-1) + `{"$eq":1}` + strings.Repeat("}", api.EventSubscriptionFilterMaxDepth-1)
	parse := map[string]func(filter string) (*gregalemanifest.Manifest, error){
		"yaml": func(filter string) (*gregalemanifest.Manifest, error) {
			return gregalemanifest.ParseBytes([]byte("event_triggers:\n  - source: billing.*\n    type: invoice.paid\n    filter: '" + filter + "'\n"))
		},
		"toml": func(filter string) (*gregalemanifest.Manifest, error) {
			return gregalemanifest.ParseTOMLBytes([]byte("[[triggers.event]]\nsource = \"billing.*\"\ntype = \"invoice.paid\"\nfilter = '" + filter + "'\n"))
		},
	}
	cases := []struct {
		name   string
		filter string
		bound  eventfilter.Bound
	}{
		{name: "typical", filter: `{"data":{"amount":{"$gt":100}}}`},
		{name: "at depth cap", filter: atDepth},
		{name: "too large", filter: large, bound: eventfilter.BoundBytes},
		{name: "too deep", filter: deep, bound: eventfilter.BoundDepth},
	}
	for format, parseFilter := range parse {
		for _, tc := range cases {
			t.Run(format+"/"+tc.name, func(t *testing.T) {
				m, err := parseFilter(tc.filter)
				if err != nil {
					t.Fatalf("parse: %v", err)
				}
				err = m.ValidateForPlan(api.PlanScale)
				var bound *eventfilter.BoundError
				if tc.bound == "" {
					if err != nil {
						t.Fatalf("ValidateForPlan = %v, want nil", err)
					}
					return
				}
				if !errors.As(err, &bound) || bound.Bound != tc.bound {
					t.Fatalf("ValidateForPlan = %v, want a %s bound error", err, tc.bound)
				}
				if _, convertErr := m.EventTriggers[0].AsSubscription("00000000-0000-0000-0000-000000000001"); !errors.As(convertErr, &bound) {
					t.Fatalf("AsSubscription = %v, want a bound error", convertErr)
				}
			})
		}
	}
}
