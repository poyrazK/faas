package state

import "github.com/onebox-faas/faas/pkg/api"

func (m *MemStore) captureQueueBindingScopeLocked(app App, binding *QueueBinding) error {
	if binding.DeploymentScope == "" {
		if binding.EnvironmentID != "" {
			return ErrInvalidArgument
		}
		return nil
	}
	if !api.ValidProjectEnvironmentSlug(binding.DeploymentScope) || app.ProjectID == "" {
		return ErrInvalidArgument
	}
	environment, err := m.projectEnvironmentBySlugLocked(app.ProjectID, binding.DeploymentScope)
	if err != nil || environment.AccountID != app.AccountID || binding.EnvironmentID != "" && canonicalMemUUID(binding.EnvironmentID) != canonicalMemUUID(environment.ID) {
		return ErrInvalidArgument
	}
	binding.EnvironmentID = environment.ID
	return nil
}

func (m *MemStore) queueBindingEnvironmentAvailableLocked(binding QueueBinding) bool {
	if binding.DeploymentScope == "" {
		return true
	}
	app, ok := m.apps[binding.AppID]
	if !ok || app.ProjectID == "" {
		return false
	}
	environment, err := m.projectEnvironmentBySlugLocked(app.ProjectID, binding.DeploymentScope)
	return err == nil && environment.AccountID == binding.AccountID && environment.ID == binding.EnvironmentID
}

func (m *MemStore) queueBindingEnvironmentHeldLocked(inv Invocation) bool {
	if inv.Source != InvocationQueue || inv.QueueBindingID == "" {
		return false
	}
	binding, ok := m.queueBindings[inv.QueueBindingID]
	return ok && !m.queueBindingEnvironmentAvailableLocked(binding)
}
