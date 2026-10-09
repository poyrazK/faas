package state

import "context"

func (m *MemStore) captureCloneBindingsLocked(accountID, appID, scope string, values projectCloneWorkloadValues) (ProjectEnvironmentCloneBindingDefinitions, error) {
	definitions := ProjectEnvironmentCloneBindingDefinitions{}
	for _, secret := range values.Secrets {
		// Managed PostgreSQL's memory catalogue belongs to a separate store.
		// Never synthesize a provider definition from a secret ownership ID.
		if secret.ManagedPostgresBindingID != "" {
			return definitions, ErrProjectEnvironmentCloneBindingCaptureUnavailable
		}
	}
	for _, bucket := range m.objectBuckets {
		if bucket.AccountID != accountID || bucket.AppID != appID || bucket.Scope != scope || bucket.State == "deleted" {
			continue
		}
		if bucket.State != "ready" {
			return definitions, ErrProjectEnvironmentCloneBindingCapture
		}
		capture := ProjectEnvironmentCloneObjectBucket{ID: bucket.ID, Name: bucket.Name, Region: bucket.Region, BackendID: bucket.BackendID,
			BackendFingerprint: bucket.BackendFingerprint, PhysicalName: bucket.PhysicalName, PublicRead: bucket.PublicRead, ServeAt: bucket.ServeAt}
		for _, c := range m.objectS3Credentials {
			if c.AccountID == accountID && c.BucketID == bucket.ID && c.Status == ObjectS3CredentialStatusActive && c.RotationParentID == "" {
				capture.Credentials = append(capture.Credentials, ProjectEnvironmentCloneObjectCredential{ID: c.ID, Label: c.Label, Permission: c.Permission,
					ManagedAppID: c.ManagedAppID, ManagedScope: c.ManagedScope, ManagedPrefix: c.ManagedPrefix})
			}
		}
		for _, grant := range m.objectAccessGrants {
			if grant.AccountID == accountID && grant.BucketID == bucket.ID {
				capture.AccessGrants = append(capture.AccessGrants, ProjectEnvironmentCloneObjectAccessGrant{APIKeyID: grant.APIKeyID, Permission: grant.Permission})
			}
		}
		definitions.Buckets = append(definitions.Buckets, capture)
	}
	return normalizeCloneBindingDefinitions(appID, scope, values, definitions)
}

func (m *MemStore) ProjectEnvironmentCloneBindings(_ context.Context, accountID, projectID, operationID string) ([]ProjectEnvironmentCloneBindings, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	op, ok := m.projectEnvironmentCloneOperations[operationID]
	if !ok || op.AccountID != accountID || op.ProjectID != projectID {
		return nil, ErrNotFound
	}
	records := make([]projectCloneWorkloadRecord, 0, len(m.projectEnvironmentCloneWorkloads[operationID]))
	for _, record := range m.projectEnvironmentCloneWorkloads[operationID] {
		records = append(records, record)
	}
	return cloneBindingViews(records)
}
