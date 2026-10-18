package state

import (
	"context"
	"encoding/json"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
	"strconv"
)

type MCPAdmissionReader interface {
	GetMCPAdmissionReceipt(context.Context, string, string) (api.MCPAdmissionEvidence, error)
	ListMCPAdmissionHistory(context.Context, string, string, int64, string, int) (api.MCPAdmissionHistoryResponse, error)
}

func (s *PgStore) GetMCPAdmissionReceipt(ctx context.Context, accountID, id string) (api.MCPAdmissionEvidence, error) {
	var out api.MCPAdmissionEvidence
	data, err := sqlc.New().GetMCPAdmissionReceipt(ctx, s.pool, sqlc.GetMCPAdmissionReceiptParams{AccountID: mustPgUUID(accountID), EvidenceID: id})
	if err != nil {
		return out, mapErr(err)
	}
	var envelope struct {
		Evidence api.MCPAdmissionEvidence `json:"mcp_admission"`
	}
	err = json.Unmarshal(data, &envelope)
	out = envelope.Evidence
	return out, err
}
func (s *PgStore) ListMCPAdmissionHistory(ctx context.Context, accountID, deploymentID string, before int64, outcome string, limit int) (api.MCPAdmissionHistoryResponse, error) {
	out := api.MCPAdmissionHistoryResponse{Items: []api.MCPAdmissionEvidence{}, Limit: limit}
	rows, err := sqlc.New().ListMCPAdmissionHistory(ctx, s.pool, sqlc.ListMCPAdmissionHistoryParams{AccountID: mustPgUUID(accountID), DeploymentID: mustPgUUID(deploymentID), BeforeID: before, Outcome: outcome, PageLimit: int32(limit + 1)})
	if err != nil {
		return out, err
	}
	for i, row := range rows {
		if i == limit {
			out.NextCursor = strconv.FormatInt(rows[i-1].ID, 10)
			break
		}
		var evidence api.MCPAdmissionEvidence
		var envelope struct {
			Evidence api.MCPAdmissionEvidence `json:"mcp_admission"`
		}
		if err = json.Unmarshal(row.Evidence, &envelope); err != nil {
			return out, err
		}
		evidence = envelope.Evidence
		out.Items = append(out.Items, evidence)
	}
	return out, nil
}
func (m *MemStore) GetMCPAdmissionReceipt(_ context.Context, accountID, id string) (api.MCPAdmissionEvidence, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, row := range m.deploymentAudit {
		evidence, ok := m.mcpAdmissionOwnedLocked(row, accountID)
		if ok && evidence.ID == id {
			return evidence, nil
		}
	}
	return api.MCPAdmissionEvidence{}, ErrNotFound
}
func (m *MemStore) mcpAdmissionOwnedLocked(row DeploymentAudit, accountID string) (api.MCPAdmissionEvidence, bool) {
	var data struct {
		Evidence api.MCPAdmissionEvidence `json:"mcp_admission"`
	}
	if row.Kind != DeployMCPAdmission || row.AccountID == nil || mustPgUUID(row.AccountID.String()) != mustPgUUID(accountID) {
		return data.Evidence, false
	}
	d, ok := m.deployments[row.DeploymentID.String()]
	if !ok { // MemStore deployment keys can be compact UUIDs.
		for _, candidate := range m.deployments {
			if mustPgUUID(candidate.ID) == mustPgUUID(row.DeploymentID.String()) {
				d = candidate
				ok = true
				break
			}
		}
	}
	if !ok {
		return data.Evidence, false
	}
	a, ok := m.apps[d.AppID]
	if !ok || a.AccountID != accountID {
		return data.Evidence, false
	}
	if json.Unmarshal(row.Data, &data) != nil {
		return data.Evidence, false
	}
	return data.Evidence, true
}
func (m *MemStore) ListMCPAdmissionHistory(_ context.Context, accountID, deploymentID string, before int64, outcome string, limit int) (api.MCPAdmissionHistoryResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := api.MCPAdmissionHistoryResponse{Items: []api.MCPAdmissionEvidence{}, Limit: limit}
	var last int64
	for i := len(m.deploymentAudit) - 1; i >= 0; i-- {
		row := m.deploymentAudit[i]
		if mustPgUUID(row.DeploymentID.String()) != mustPgUUID(deploymentID) || (before > 0 && row.ID >= before) {
			continue
		}
		e, ok := m.mcpAdmissionOwnedLocked(row, accountID)
		if !ok || (outcome != "" && e.Outcome != outcome) {
			continue
		}
		if len(out.Items) == limit {
			out.NextCursor = strconv.FormatInt(last, 10)
			break
		}
		out.Items = append(out.Items, e)
		last = row.ID
	}
	return out, nil
}
