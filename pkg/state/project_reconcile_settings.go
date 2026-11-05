package state

// Match the source-owned columns written by ApplyProjectReconcile. Applying
// only these fields to an existing head preserves environment-owned policies.
func projectReconcileSettingsParams(app App, mutation ProjectReconcileMutation) UpdateAppParams {
	params := UpdateAppParams{RootDir: &app.RootDir, WorkloadClass: &app.WorkloadClass,
		StartCommand: &app.StartCommand, Manifest: &app.Manifest,
		PlatformTenantRequired: &app.PlatformTenantRequired, SetPlatformTenantRequired: mutation.SetPlatformTenantRequired}
	if mutation.SetResources {
		params.RAMMB, params.CPUMillicores, params.MaxConcurrency = &app.RAMMB, &app.CPUMillicores, &app.MaxConcurrency
	}
	return params
}
