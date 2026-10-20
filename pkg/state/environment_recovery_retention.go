package state

// adr: 20261017190807. Policy selection is count/time/byte bounded. Reads never acquire pins.
import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type EnvironmentRecoveryQuotaError struct{ Observed int64 }

func (e *EnvironmentRecoveryQuotaError) Error() string {
	return fmt.Sprintf("recovery retention reservation %d exceeds account limit %d", e.Observed, api.EnvironmentRecoveryAccountMaxBytes)
}
func (e *EnvironmentRecoveryQuotaError) Unwrap() error { return ErrQuotaExceeded }

type recoveryProtection struct {
	releaseID, graphID, status string
	until                      time.Time
	served                     time.Time
	bytes                      int64
	keys                       []string
	available                  bool
}

func recoveryPolicy(memory *environmentGitOpsMemory) *api.EnvironmentRecoveryRetention {
	if memory == nil || memory.source.Detached {
		return nil
	}
	var definition api.EnvironmentDefinition
	if json.Unmarshal(memory.revisions[memory.source.ApprovedRevisionID].Definition, &definition) != nil {
		return nil
	}
	return definition.RecoveryRetention
}

// Rows are ordered newest first by original serving completion. The newest
// release has no deadline while it is current; each older one is protected
// for the policy age after the next release first completed serving, so a
// long-running release is still recoverable right after it is replaced.
func selectRecoveryProtection(rows []recoveryProtection, policy *api.EnvironmentRecoveryRetention, now time.Time) []recoveryProtection {
	slices.SortFunc(rows, func(a, b recoveryProtection) int {
		if c := b.served.Compare(a.served); c != 0 {
			return c
		}
		return strings.Compare(b.releaseID, a.releaseID)
	})
	var position int
	var charged int64
	var newer *time.Time // first serving of the next newer release
	for i := range rows {
		row := &rows[i]
		row.status, row.until = "disabled", time.Time{}
		replaced := newer
		served := row.served
		newer = &served
		if policy == nil {
			continue
		}
		if replaced != nil {
			row.until = replaced.Add(time.Duration(policy.Seconds) * time.Second)
			if !row.until.After(now) {
				row.status = "expired"
				continue
			}
		}
		position++
		charged = min(policy.MaxBytes+1, charged+min(row.bytes, policy.MaxBytes+1))
		row.status = "outside_policy"
		if position > policy.Releases {
			continue
		}
		row.status = "budget_exceeded"
		if charged > policy.MaxBytes {
			continue
		}
		row.status = "protected"
	}
	return rows
}

func (m *MemStore) recoveryProtectionLocked(memory *environmentGitOpsMemory, policy *api.EnvironmentRecoveryRetention, now time.Time) []recoveryProtection {
	if memory == nil || memory.source.Detached {
		return nil
	}
	source := memory.source
	rows := map[string]recoveryProtection{}
	for _, g := range memory.graphs {
		receipt := m.environmentWorkloadServingReceipts[g.ID]
		r := m.projectReleaseSets[receipt.ReleaseSetID]
		if historicalServingAt(receipt) == nil || g.EnvironmentID != source.EnvironmentID || r.AccountID != source.AccountID || r.ProjectID != source.ProjectID || r.EnvironmentSlug != source.EnvironmentSlug {
			continue
		}
		if len(r.Members) == 0 {
			continue
		}
		if prior, exists := rows[r.ID]; exists {
			prior.available = false
			if historicalServingAt(receipt).After(prior.served) {
				prior.served = *historicalServingAt(receipt)
			}
			if g.ID < prior.graphID {
				prior.graphID = g.ID
			}
			rows[r.ID] = prior
			continue
		}
		row := recoveryProtection{releaseID: r.ID, graphID: g.ID, served: *historicalServingAt(receipt), available: len(r.Members) > 0}
		keys := map[string]int64{}
		for _, member := range r.Members {
			d := m.deployments[member.DeploymentID]
			a := m.apps[d.AppID]
			row.available = row.available && d.ID != "" && a.AccountID == source.AccountID && a.ProjectID == source.ProjectID && d.AppID == member.AppID && normalizedDeploymentScope(d.Scope) == source.EnvironmentSlug && validLayerArtifactKey(d.RootfsKey) && d.RootfsBytes > 0 && d.DeletedAt == nil && a.Status != AppDeleted
			keys[d.RootfsKey] = max(keys[d.RootfsKey], d.RootfsBytes)
			for _, layer := range m.deploymentSidecarLayers {
				if layer.DeploymentID != d.ID {
					continue
				}
				row.available = row.available && validLayerArtifactKey(layer.StorageKey) && layer.Bytes > 0
				keys[layer.StorageKey] = max(keys[layer.StorageKey], layer.Bytes)
			}
		}
		for key, bytes := range keys {
			row.keys = append(row.keys, key)
			row.bytes = min(api.EnvironmentRecoveryMaxBytes+1, row.bytes+min(max(bytes, 0), api.EnvironmentRecoveryMaxBytes+1))
		}
		slices.Sort(row.keys)
		rows[r.ID] = row
	}
	out := make([]recoveryProtection, 0, len(rows))
	for _, row := range rows {
		out = append(out, row)
	}
	return selectRecoveryProtection(out, policy, now)
}

func (m *MemStore) validateRecoveryRetentionApprovalLocked(source EnvironmentGitSource, definition api.EnvironmentDefinition) error {
	var reserved int64
	for _, memory := range m.environmentGitOps {
		p := recoveryPolicy(memory)
		if memory.source.ID == source.ID {
			p = definition.RecoveryRetention
		}
		if memory.source.AccountID == source.AccountID && !memory.source.Detached && p != nil {
			reserved += p.MaxBytes
		}
	}
	if reserved > api.EnvironmentRecoveryAccountMaxBytes {
		return &EnvironmentRecoveryQuotaError{Observed: reserved}
	}
	var keys []string
	for _, row := range m.recoveryProtectionLocked(m.environmentGitOps[source.ID], definition.RecoveryRetention, time.Now().UTC()) {
		if row.status != "protected" {
			continue
		}
		if !row.available {
			return ErrLayerArtifactRetired
		}
		keys = append(keys, row.keys...)
	}
	// No partial mutation when even one selected key has been claimed by GC.
	for _, key := range keys {
		if row, ok := m.layerArtifactRetention[key]; ok && row.State != LayerArtifactRetained {
			return ErrLayerArtifactRetired
		}
	}
	return m.requireLayerArtifactsRetainedLocked(keys)
}

func (m *MemStore) recoveryProtectedDeploymentLocked(id string) bool {
	for _, memory := range m.environmentGitOps {
		p := recoveryPolicy(memory)
		if p == nil {
			continue
		}
		for _, row := range m.recoveryProtectionLocked(memory, p, time.Now().UTC()) {
			if row.status != "protected" {
				continue
			}
			for _, member := range m.projectReleaseSets[row.releaseID].Members {
				if member.DeploymentID == id {
					return true
				}
				if b := candidateFrozenInputs(m.deployments[member.DeploymentID]).BuiltArtifact; b != nil && b.DeploymentID == id {
					return true
				}
			}
		}
	}
	return false
}

func applyRecoveryProtection(entry *api.EnvironmentGitRecoveryRelease, rows []recoveryProtection) {
	for _, row := range rows {
		if row.releaseID == entry.ReleaseSetID {
			entry.ProtectionStatus = row.status
			if row.status == "protected" {
				// The current release has no deadline until it is replaced.
				if !row.until.IsZero() {
					until := row.until
					entry.ArtifactsProtectedUntil = &until
				}
				entry.RetainedBytes = row.bytes
			}
			return
		}
	}
}

func (m *MemStore) publishRecoveryServingReceiptLocked(receipt EnvironmentWorkloadServingReceipt) error {
	old, exists := m.environmentWorkloadServingReceipts[receipt.GraphID]

	if exists && historicalServingAt(old) != nil {
		if old.ReleaseSetID != receipt.ReleaseSetID {
			return ErrConflict
		}
		first := *historicalServingAt(old)
		receipt.FirstServedAt = &first
	} else if receipt.ServedAt != nil {
		first := *receipt.ServedAt
		receipt.FirstServedAt = &first
	}
	m.environmentWorkloadServingReceipts[receipt.GraphID] = cloneEnvironmentWorkloadServingReceipt(receipt)
	for _, memory := range m.environmentGitOps {
		if _, ok := memory.graphs[receipt.GraphID]; !ok {
			continue
		}
		p := recoveryPolicy(memory)
		if p == nil {
			continue
		}
		var definition api.EnvironmentDefinition
		definition.RecoveryRetention = p
		if err := m.validateRecoveryRetentionApprovalLocked(memory.source, definition); err != nil {
			if exists {
				m.environmentWorkloadServingReceipts[receipt.GraphID] = old
			} else {
				delete(m.environmentWorkloadServingReceipts, receipt.GraphID)
			}
			return err
		}
	}
	return nil
}

func (m *MemStore) recoveryProtectedScopeLocked(account, project, environment string) bool {
	for _, memory := range m.environmentGitOps {
		s := memory.source
		if account != "" && s.AccountID != account || project != "" && s.ProjectID != project || environment != "" && s.EnvironmentSlug != environment {
			continue
		}
		p := recoveryPolicy(memory)
		if p == nil {
			continue
		}
		for _, row := range m.recoveryProtectionLocked(memory, p, time.Now().UTC()) {
			if row.status == "protected" {
				return true
			}
		}
	}
	return false
}

func historicalServingAt(receipt EnvironmentWorkloadServingReceipt) *time.Time {
	if receipt.FirstServedAt != nil {
		return receipt.FirstServedAt
	}
	return receipt.ServedAt
}
