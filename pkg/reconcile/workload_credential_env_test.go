package reconcile

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/capgrant"
	"github.com/onebox-faas/faas/pkg/reposcan"
)

// The platform stamps the workload credential endpoint only for a service
// with exactly one bucket grant: the endpoint without ?bucket= cannot pick
// between several (ADR-20261122191423 slice 2).
func TestWorkloadCredentialEnv(t *testing.T) {
	grants := func(names ...string) reposcan.Workload {
		uses := &capgrant.Uses{}
		for _, name := range names {
			uses.Buckets = append(uses.Buckets, capgrant.BucketGrant{Bucket: name, App: "worker", Access: "read"})
		}
		return reposcan.Workload{Uses: uses}
	}
	const custom = api.WorkloadObjectCredentialsEndpoint + "?bucket=thumbs"
	for name, tc := range map[string]struct {
		base map[string]string
		w    reposcan.Workload
		want string
	}{
		"one grant":                 {nil, grants("assets"), api.WorkloadObjectCredentialsEndpoint},
		"two grants":                {nil, grants("assets", "thumbs"), ""},
		"two grants drop platform":  {map[string]string{api.WorkloadCredentialsEnv: api.WorkloadObjectCredentialsEndpoint}, grants("assets", "thumbs"), ""},
		"no grants":                 {nil, reposcan.Workload{}, ""},
		"customer value kept":       {map[string]string{api.WorkloadCredentialsEnv: custom}, grants("assets", "thumbs"), custom},
		"customer value, no grants": {map[string]string{api.WorkloadCredentialsEnv: custom}, reposcan.Workload{}, custom},
	} {
		if got := workloadCredentialEnv(tc.base, tc.w)[api.WorkloadCredentialsEnv]; got != tc.want {
			t.Errorf("%s: %q, want %q", name, got, tc.want)
		}
	}
}

// A customer's own endpoint is left in place by apply, so diff must not
// report it as drift on every run.
func TestServiceEnvEqualIgnoresCustomerCredentialEndpoint(t *testing.T) {
	actual := map[string]string{api.WorkloadCredentialsEnv: api.WorkloadObjectCredentialsEndpoint + "?bucket=thumbs"}
	expected := map[string]string{api.WorkloadCredentialsEnv: api.WorkloadObjectCredentialsEndpoint}
	if !serviceEnvEqual(actual, expected) {
		t.Fatal("customer credential endpoint reported as drift")
	}
	stale := map[string]string{api.WorkloadCredentialsEnv: api.WorkloadObjectCredentialsEndpoint}
	if serviceEnvEqual(stale, map[string]string{}) {
		t.Fatal("platform endpoint left after its grant was dropped is not drift")
	}
}
