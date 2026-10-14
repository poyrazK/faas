package apidserver_test

import (
	"reflect"
	"testing"

	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

// App errors return the public request ID in dashed UUID form; the gateway
// issued and journaled it as 32 hex characters.
func TestDebugRequestIDCandidates(t *testing.T) {
	for name, tc := range map[string]struct {
		in   string
		want []string
	}{
		"compact public id": {"a274ed687a4be17dda84779332517e1f", []string{"a274ed687a4be17dda84779332517e1f"}},
		"dashed app-error id": {"a274ed68-7a4b-e17d-da84-779332517e1f",
			[]string{"a274ed68-7a4b-e17d-da84-779332517e1f", "a274ed687a4be17dda84779332517e1f"}},
		"customer-supplied id": {"order-123-retry", []string{"order-123-retry"}},
	} {
		t.Run(name, func(t *testing.T) {
			if got := apidserver.DebugRequestIDCandidates(tc.in); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("candidates(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}
