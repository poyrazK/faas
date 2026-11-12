package eventfilter_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/eventfilter"
)

// nested returns a filter of the given object depth: {"a":{"a":...{"$eq":1}}}.
func nested(depth int) string {
	return strings.Repeat(`{"a":`, depth-1) + `{"$eq":1}` + strings.Repeat("}", depth-1)
}

// sized returns a one-key filter whose compact JSON is exactly n bytes.
func sized(n int) string {
	const overhead = len(`{"k":{"$eq":""}}`)
	return `{"k":{"$eq":"` + strings.Repeat("x", n-overhead) + `"}}`
}

// Events hunt #9: every recipient snapshot copies its subscription filter, so
// the write path caps a filter's compact size and nesting depth.
func TestValidateBoundedFilter(t *testing.T) {
	const maxBytes, maxDepth = 64, 4
	cases := []struct {
		name     string
		filter   string
		bound    eventfilter.Bound
		observed int
		invalid  bool
	}{
		{name: "empty", filter: ""},
		{name: "null", filter: "null"},
		{name: "typical", filter: `{"data":{"amount":{"$gt":100}}}`},
		{name: "at byte cap", filter: sized(maxBytes)},
		{name: "one byte over", filter: sized(maxBytes + 1), bound: eventfilter.BoundBytes, observed: maxBytes + 1},
		{name: "whitespace is not counted", filter: "  " + strings.ReplaceAll(sized(maxBytes), ":", " : ") + "\n"},
		{name: "at depth cap", filter: nested(maxDepth)},
		{name: "one level too deep", filter: nested(maxDepth + 1), bound: eventfilter.BoundDepth, observed: maxDepth + 1},
		{name: "arrays count as levels", filter: `{"a":{"$eq":[[[1]]]}}`, bound: eventfilter.BoundDepth, observed: 5},
		{name: "operators still validated", filter: `{"a":{"$regex":"x"}}`, invalid: true},
		{name: "malformed", filter: `{"a":`, invalid: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := eventfilter.ValidateBoundedFilter(json.RawMessage(tc.filter), maxBytes, maxDepth)
			var bound *eventfilter.BoundError
			switch {
			case tc.bound != "":
				if !errors.As(err, &bound) || bound.Bound != tc.bound || bound.Limit == 0 || bound.Observed != tc.observed {
					t.Fatalf("err = %v, want %s bound error observing %d", err, tc.bound, tc.observed)
				}
			case tc.invalid:
				if err == nil || errors.As(err, &bound) {
					t.Fatalf("err = %v, want a validation error that is not a bound", err)
				}
			case err != nil:
				t.Fatalf("err = %v, want nil", err)
			}
		})
	}
}

// Evaluation keeps accepting stored filters that predate the caps.
func TestValidateFilterIsUnbounded(t *testing.T) {
	if err := eventfilter.ValidateFilter(json.RawMessage(nested(64))); err != nil {
		t.Fatalf("ValidateFilter(deep) = %v, want nil", err)
	}
}
