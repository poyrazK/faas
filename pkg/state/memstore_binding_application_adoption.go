package state

import (
	"context"
	"sort"

	"github.com/onebox-faas/faas/pkg/api"
)

var _ BindingApplicationAdoptionStore = (*MemStore)(nil)

func (m *MemStore) ReadBindingApplicationAdoption(_ context.Context, accountID, appID string, selectors []BindingAdoptionSelector) ([]BindingApplicationAdoptionRow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rows := []BindingApplicationAdoptionRow{}
	for _, selector := range selectors {
		for _, key := range selector.Keys {
			secret, ok := m.secrets[secretKey{AppID: appID, Scope: selector.Scope, Key: key}]
			if !ok || secret.AccountID != accountID || !bindingAdoptionSecretOwned(secret, selector) {
				continue
			}
			base := BindingApplicationAdoptionRow{Type: selector.Type, BindingID: selector.BindingID, Scope: selector.Scope, Key: key, CurrentVersion: secret.DeliveryVersion}
			before := len(rows)
			for _, instance := range m.instances {
				if instance.AppID != appID || instance.Kind != "" && instance.Kind != "wake" || instance.Mode == string(InstanceModeMirror) || !State(instance.State).CountsForRAM() {
					continue
				}
				targets, err := m.secretRevocationTargetsForInstanceLocked(secret, instance)
				if err != nil {
					return nil, err
				}
				for _, target := range targets {
					row := base
					row.DeploymentID, row.InstanceID, row.WorkloadName = instance.DeploymentID, instance.ID, target.WorkloadName
					row.RuntimeState, row.ReloadSupport = target.RuntimeState, target.ReloadSupport
					observation := m.secretRuntimeReloadObservations[secretRuntimeReloadObservationKey{AppID: appID, Scope: selector.Scope, Key: key, InstanceID: instance.ID, WorkloadName: target.WorkloadName}]
					row.ReloadVersion, row.Projection, row.Signal = observation.Version, string(observation.Projection), string(observation.Signal)
					if !observation.ObservedAt.IsZero() {
						row.ReloadAt = cloneHealthTime(&observation.ObservedAt)
					}
					row.ApplicationAckVersion, row.ApplicationAck, row.ApplicationAckAt = observation.ApplicationAckVersion, string(observation.ApplicationAck), cloneHealthTime(observation.ApplicationAckAt)
					process := m.secretRuntimeProcesses[secretRuntimeProcessKey{InstanceID: instance.ID, WorkloadName: target.WorkloadName}]
					if process.Active {
						row.ProcessGeneration = process.Generation
					}
					row.ApplicationAckGeneration = observation.ApplicationAckGeneration
					rows = append(rows, row)
				}
			}
			if len(rows) == before {
				rows = append(rows, base)
			}
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		return rows[i].Type+rows[i].BindingID+rows[i].Key+rows[i].InstanceID+rows[i].WorkloadName < rows[j].Type+rows[j].BindingID+rows[j].Key+rows[j].InstanceID+rows[j].WorkloadName
	})
	return rows, nil
}

func bindingAdoptionSecretOwned(secret AppSecret, selector BindingAdoptionSelector) bool {
	switch selector.Type {
	case api.BindingTypePostgres:
		return secret.ManagedPostgresBindingID == selector.BindingID && secret.ManagedObjectStorageCredentialID == ""
	case api.BindingTypeObjectStorage:
		return secret.ManagedObjectStorageCredentialID == selector.BindingID && secret.ManagedPostgresBindingID == ""
	default:
		return false
	}
}
