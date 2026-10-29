package imaged

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// checkCompanionPorts compares each companion's declared port with the main
// workload's final port. apid can only guess the main port at submission
// unless the deploy sets one: the build decides it from the image's EXPOSE,
// gregale.yaml's hosting.port or the detected framework. A gunicorn app on
// 8000 with an nginx companion on 8080 was refused, while a companion on 8000
// would have been accepted and collided at runtime (Hunt 14 H14-53).
func checkCompanionPorts(manifest api.AppManifest, dep state.Deployment) error {
	if len(dep.Sidecars) == 0 || string(dep.Sidecars) == "null" {
		return nil
	}
	var companions api.Sidecars
	if err := json.Unmarshal(dep.Sidecars, &companions); err != nil {
		return fmt.Errorf("decode companions: %w", err)
	}
	if problem := companions.ValidatePortConflicts(manifest.Port); problem != nil {
		return errors.New(problem.Detail)
	}
	return nil
}
