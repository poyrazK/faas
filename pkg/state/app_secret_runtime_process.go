// adr:438
package state

import (
	"context"
	"encoding/hex"
	"strings"
	"time"
)

// Identity is supplied by vmmd's instance-bound transport, never the app ACK.
type AppSecretRuntimeProcess struct {
	AccountID, AppID, InstanceID, WorkloadName string
	Generation, PreviousGeneration             string
	AttemptedAt                                time.Time
}

type secretRuntimeProcessKey struct{ InstanceID, WorkloadName string }
type appSecretRuntimeProcess struct {
	AppID, Generation string
	Active            bool
	StartedAt         time.Time
}

func ValidSecretProcessGeneration(generation string) bool {
	if len(generation) != 32 || strings.ToLower(generation) != generation {
		return false
	}
	_, err := hex.DecodeString(generation)
	return err == nil
}

func validAppSecretRuntimeProcess(p AppSecretRuntimeProcess) bool {
	return p.AccountID != "" && p.AppID != "" && p.InstanceID != "" && ValidSecretRuntimeWorkloadName(p.WorkloadName) &&
		ValidSecretProcessGeneration(p.Generation) && (p.PreviousGeneration == "" || ValidSecretProcessGeneration(p.PreviousGeneration))
}

func secretRuntimeProcessAllowsAck(current string, active bool, supplied string) bool {
	return current == "" && supplied == "" || current != "" && active && supplied == current
}

func (m *MemStore) BeginAppSecretRuntimeProcess(_ context.Context, p AppSecretRuntimeProcess) error {
	if !validAppSecretRuntimeProcess(p) {
		return ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.secretRuntimeProcessOwnedLocked(p) {
		return ErrConflict
	}
	key := secretRuntimeProcessKey{InstanceID: p.InstanceID, WorkloadName: p.WorkloadName}
	current := m.secretRuntimeProcesses[key]
	if current.Generation == p.Generation {
		if !current.Active {
			return ErrConflict
		}
		return nil
	}
	if current.Generation != "" && current.Generation != p.PreviousGeneration {
		return ErrConflict
	}
	at := p.AttemptedAt.UTC()
	if at.IsZero() {
		at = time.Now().UTC()
	}
	m.secretRuntimeProcesses[key] = appSecretRuntimeProcess{AppID: p.AppID, Generation: p.Generation, Active: true, StartedAt: at}
	m.clearSecretRuntimeProcessAckLocked(p)
	return nil
}

func (m *MemStore) RetireAppSecretRuntimeProcess(_ context.Context, p AppSecretRuntimeProcess) error {
	if !validAppSecretRuntimeProcess(p) || p.PreviousGeneration != "" {
		return ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.secretRuntimeProcessOwnedLocked(p) {
		return ErrConflict
	}
	key := secretRuntimeProcessKey{InstanceID: p.InstanceID, WorkloadName: p.WorkloadName}
	current := m.secretRuntimeProcesses[key]
	if current.Generation != p.Generation {
		return ErrConflict
	}
	if !current.Active {
		return nil
	}
	current.Active = false
	m.secretRuntimeProcesses[key] = current
	m.clearSecretRuntimeProcessAckLocked(p)
	return nil
}

func (m *MemStore) secretRuntimeProcessOwnedLocked(p AppSecretRuntimeProcess) bool {
	instance, ok := m.instances[p.InstanceID]
	return ok && instance.AppID == p.AppID && m.apps[p.AppID].AccountID == p.AccountID
}

func (m *MemStore) clearSecretRuntimeProcessAckLocked(p AppSecretRuntimeProcess) {
	for key, observation := range m.secretRuntimeReloadObservations {
		if key.AppID != p.AppID || key.InstanceID != p.InstanceID || key.WorkloadName != p.WorkloadName {
			continue
		}
		observation.ApplicationAckVersion, observation.ApplicationAck, observation.ApplicationAckAt = 0, "", nil
		observation.ApplicationAckErrorCode, observation.ApplicationAckGeneration = "", ""
		m.secretRuntimeReloadObservations[key] = observation
	}
}

func (m *MemStore) deleteSecretRuntimeProcessesLocked(instance string) {
	for key := range m.secretRuntimeProcesses {
		if key.InstanceID == instance {
			delete(m.secretRuntimeProcesses, key)
		}
	}
}
