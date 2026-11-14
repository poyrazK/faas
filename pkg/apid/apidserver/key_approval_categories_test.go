package apidserver_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

// Hunt 33 H33-9: `key create --scopes apps:read --approvals none` was refused
// with "approval_categories apply only to keys with the agent scope".
func TestKeyApprovalCategoriesNoneOnANonAgentKey(t *testing.T) {
	for _, tc := range []struct {
		name      string
		scopes    []string
		requested []string
		wantSet   bool
		wantErr   bool
	}{
		{"none on a read key", []string{"apps:read"}, []string{"none"}, false, false},
		{"a category on a read key", []string{"apps:read"}, []string{"delete"}, false, true},
		{"unknown category", []string{"apps:read"}, []string{"bogus"}, false, true},
		{"none on an agent key", []string{api.ScopeAgent}, []string{"none"}, true, false},
		{"a category on an agent key", []string{api.ScopeAgent}, []string{"delete"}, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requested := tc.requested
			_, set, prob := apidserver.KeyApprovalCategories(tc.scopes, &requested)
			if set != tc.wantSet || (prob != nil) != tc.wantErr {
				t.Fatalf("set=%t problem=%v, want set=%t error=%t", set, prob, tc.wantSet, tc.wantErr)
			}
		})
	}
}
