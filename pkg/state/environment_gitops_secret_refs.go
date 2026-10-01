package state

import (
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/environmentsync"
)

func validateGitOpsSecretRefs(out *EnvironmentGitOpsObservation, desired environmentsync.DesiredState, resource string, app gitOpsIntentApp) {
	names := map[string]bool{}
	for _, name := range app.SecretNames {
		names[name] = true
	}
	workload := desired.Definition.Workloads[strings.TrimPrefix(resource, "workload/")]
	for key, ref := range workload.SecretRefs {
		if !names[strings.TrimPrefix(ref, api.SecretRefPrefix)] {
			out.State.Unsupported = append(out.State.Unsupported, resource+"#secret_refs/"+key+": referenced secret is unavailable in this environment")
		}
		if _, exists := app.Variables[key]; exists {
			out.State.Unsupported = append(out.State.Unsupported, resource+"#secret_refs/"+key+": remove the non-secret variable before adopting this reference")
		}
	}
}
