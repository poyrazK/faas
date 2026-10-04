// adr: 531
package gateway

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

type requiredOwnerPolicyMatcher struct{ snapshotTestMatcher }

func (requiredOwnerPolicyMatcher) RequiresOwnerPolicySnapshot() bool { return true }

func TestOwnerPolicyAdmissionRefusesIncompleteCarrier(t *testing.T) {
	for _, missing := range []string{"baseline", "revision", "host", "seal", "carrier"} {
		t.Run(missing, func(t *testing.T) {
			h, backend, _ := newTestHandler(t)
			entry := &HostEntry{Host: backend.host, PublicSourceRevision: "verified"}
			if err := entry.SealPolicy(); err != nil {
				t.Fatal(err)
			}
			h.WithEdgeRules(requiredOwnerPolicyMatcher{snapshotTestMatcher{entry: entry}}, nil, nil)
			r := httptest.NewRequest(http.MethodGet, "http://"+backend.host+"/", nil)
			ctx, err := WithPinnedHostPolicy(r.Context(), backend.host, entry)
			if err != nil {
				t.Fatal(err)
			}
			r = r.WithContext(ctx)
			app := backend.app
			app.PublicPolicySource = &PublicAppPolicySource{Revision: "verified"}
			app.PublicCompiledPolicies = map[string]*HostEntry{backend.host: entry}
			switch missing {
			case "baseline":
				app.PublicPolicySource = nil
			case "revision":
				app.PublicCompiledPolicies[backend.host] = &HostEntry{Host: backend.host}
			case "host":
				app.PublicCompiledPolicies[backend.host] = &HostEntry{Host: "other-host", PublicSourceRevision: "verified"}
			case "seal":
				app.PublicCompiledPolicies[backend.host] = &HostEntry{Host: backend.host, PublicSourceRevision: "verified"}
			case "carrier":
				app.PublicCompiledPolicies = nil
			}
			rec := httptest.NewRecorder()
			if !h.pinResolvedOwnerPolicies(rec, r, app) || rec.Code != http.StatusServiceUnavailable {
				t.Fatalf("incomplete owner policy admitted: %d", rec.Code)
			}
		})
	}
}
