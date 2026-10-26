package devloop

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestValidateGoCommand(t *testing.T) {
	for _, tc := range []struct {
		req api.UpsertDevSessionRequest
		ok  bool
	}{
		{api.UpsertDevSessionRequest{}, true},
		{api.UpsertDevSessionRequest{GoCommand: "worker"}, true},
		{api.UpsertDevSessionRequest{GoCommand: "worker", Type: "function"}, false},
		{api.UpsertDevSessionRequest{GoCommand: "../worker"}, false},
		{api.UpsertDevSessionRequest{GoCommand: "w'; rm -rf /"}, false},
	} {
		if got := ValidateGoCommand(tc.req) == nil; got != tc.ok {
			t.Errorf("%+v: valid = %t, want %t", tc.req, got, tc.ok)
		}
	}
}
