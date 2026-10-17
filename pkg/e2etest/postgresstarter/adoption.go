package postgresstarter

import "github.com/onebox-faas/faas/pkg/api"

// Adopted requires current public writer-binding ACKs from every authorized
// resident process, fenced to the selected deployment and credential generation.
func Adopted(inventory api.AppBindingInventory, deployment string, generation int64) bool {
	if !inventory.Complete || deployment == "" || generation < 1 {
		return false
	}
	found := false
	for _, item := range inventory.Bindings {
		if item.Type != api.BindingTypePostgres || item.Binding != "DATABASE_URL" {
			continue
		}
		if found || item.CredentialGeneration == nil || *item.CredentialGeneration != generation || item.RotationPending == nil || *item.RotationPending {
			return false
		}
		found = true
		adoption := item.ApplicationAdoption
		if adoption == nil || !adoption.Complete || adoption.Status != "current" || adoption.SecretsExpected != 1 ||
			adoption.SecretsObserved != 1 || len(adoption.Targets) == 0 || adoption.Application.Current != len(adoption.Targets) {
			return false
		}
		instances := map[string]bool{}
		for _, target := range adoption.Targets {
			if target.DeploymentID != deployment || target.InstanceID == "" || instances[target.InstanceID] || target.WorkloadName != "" || target.Key != "DATABASE_URL" ||
				target.CurrentVersion < 1 || target.ApplicationAckVersion != target.CurrentVersion || target.ProcessGeneration == "" || target.ApplicationAckGeneration != target.ProcessGeneration {
				return false
			}
			instances[target.InstanceID] = true
		}
	}
	return found
}
