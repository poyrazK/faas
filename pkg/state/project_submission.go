package state

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type ProjectSubmissionStore interface {
	ReserveProjectSubmission(context.Context, string, string, string, api.ProjectSubmissionReceipt) (bool, error)
	WriteProjectSubmission(context.Context, string, api.ProjectSubmissionReceipt) error
	TouchProjectSubmission(context.Context, string, string) error
	ProjectSubmission(context.Context, string, string) (api.ProjectSubmissionReceipt, error)
}
type projectSubmissionEntry struct {
	Fingerprint string
	Payload     []byte
	Status      string
	UpdatedAt   time.Time
}

var ErrProjectSubmissionConflict = errors.New("project submission intent changed")
var ErrProjectSubmissionExists = errors.New("project submission already registered")

func ProjectWorkloadAttempt(attemptID, appID string) string {
	sum := sha256.Sum256([]byte("project-workload\x00" + attemptID + "\x00" + appID))
	return hex.EncodeToString(sum[:16])
}
func (m *MemStore) ReserveProjectSubmission(ctx context.Context, accountID, attemptID, fingerprint string, r api.ProjectSubmissionReceipt) (bool, error) {
	if !api.ValidDeploymentAttemptID(attemptID) || r.AttemptID != attemptID {
		return false, ErrInvalidArgument
	}
	data, err := json.Marshal(r)
	if err != nil {
		return false, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.projectSubmissions == nil {
		m.projectSubmissions = map[string]projectSubmissionEntry{}
	}
	key := accountID + "\x00" + attemptID
	if old, ok := m.projectSubmissions[key]; ok {
		if old.Fingerprint != fingerprint {
			return false, ErrProjectSubmissionConflict
		}
		return false, nil
	}
	if !m.resourceNamespaceExistsLocked(accountID) {
		return false, ErrNotFound
	}
	m.projectSubmissions[key] = projectSubmissionEntry{Fingerprint: fingerprint, Payload: data, Status: "processing", UpdatedAt: time.Now().UTC()}
	return true, nil
}
func (m *MemStore) WriteProjectSubmission(ctx context.Context, accountID string, r api.ProjectSubmissionReceipt) error {
	data, err := json.Marshal(r)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	key := accountID + "\x00" + r.AttemptID
	old, ok := m.projectSubmissions[key]
	if !ok || old.Status != "processing" {
		return ErrProjectSubmissionConflict
	}
	m.projectSubmissions[key] = projectSubmissionEntry{Fingerprint: old.Fingerprint, Payload: data, Status: r.Status, UpdatedAt: time.Now().UTC()}
	return nil
}
func (m *MemStore) TouchProjectSubmission(ctx context.Context, accountID, attemptID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := accountID + "\x00" + attemptID
	row, ok := m.projectSubmissions[key]
	if ok && row.Status == "processing" {
		row.UpdatedAt = time.Now().UTC()
		m.projectSubmissions[key] = row
	}
	return nil
}
func (m *MemStore) ProjectSubmission(ctx context.Context, accountID, attemptID string) (api.ProjectSubmissionReceipt, error) {
	m.mu.Lock()
	entry, ok := m.projectSubmissions[accountID+"\x00"+attemptID]
	m.mu.Unlock()
	if !ok {
		return api.ProjectSubmissionReceipt{}, ErrNotFound
	}
	return hydrateProjectSubmission(ctx, m, entry, accountID)
}
func (s *PgStore) ReserveProjectSubmission(ctx context.Context, accountID, attemptID, fingerprint string, r api.ProjectSubmissionReceipt) (bool, error) {
	if !api.ValidDeploymentAttemptID(attemptID) || r.AttemptID != attemptID {
		return false, ErrInvalidArgument
	}
	data, err := json.Marshal(r)
	if err != nil {
		return false, err
	}
	n, err := sqlc.New().ReserveProjectSubmission(ctx, s.pool, sqlc.ReserveProjectSubmissionParams{AccountID: accountID, AttemptID: attemptID, Fingerprint: fingerprint, Payload: data})
	if err != nil || n == 1 {
		return n == 1, err
	}
	old, err := sqlc.New().ReadProjectSubmission(ctx, s.pool, sqlc.ReadProjectSubmissionParams{AccountID: accountID, AttemptID: attemptID})
	if err != nil {
		return false, err
	}
	if old.Fingerprint != fingerprint {
		return false, ErrProjectSubmissionConflict
	}
	return false, nil
}
func (s *PgStore) WriteProjectSubmission(ctx context.Context, accountID string, r api.ProjectSubmissionReceipt) error {
	data, err := json.Marshal(r)
	if err != nil {
		return err
	}
	n, err := sqlc.New().WriteProjectSubmission(ctx, s.pool, sqlc.WriteProjectSubmissionParams{AccountID: accountID, AttemptID: r.AttemptID, Payload: data, Status: r.Status})
	if err == nil && n != 1 {
		return ErrProjectSubmissionConflict
	}
	return err
}
func (s *PgStore) TouchProjectSubmission(ctx context.Context, accountID, attemptID string) error {
	return sqlc.New().TouchProjectSubmission(ctx, s.pool, sqlc.TouchProjectSubmissionParams{AccountID: accountID, AttemptID: attemptID})
}
func (s *PgStore) ProjectSubmission(ctx context.Context, accountID, attemptID string) (api.ProjectSubmissionReceipt, error) {
	row, err := sqlc.New().ReadProjectSubmission(ctx, s.pool, sqlc.ReadProjectSubmissionParams{AccountID: accountID, AttemptID: attemptID})
	if err != nil {
		return api.ProjectSubmissionReceipt{}, mapErr(err)
	}
	return hydrateProjectSubmission(ctx, s, projectSubmissionEntry{Payload: row.Payload, Status: row.Status, UpdatedAt: row.UpdatedAt.Time}, accountID)
}
func hydrateProjectSubmission(ctx context.Context, store interface {
	DeploymentSubmissionStore
	DeploymentByID(context.Context, string) (Deployment, error)
}, entry projectSubmissionEntry, accountID string) (api.ProjectSubmissionReceipt, error) {
	var r api.ProjectSubmissionReceipt
	if err := json.Unmarshal(entry.Payload, &r); err != nil {
		return r, err
	}
	r.Status = entry.Status
	if r.Status == "processing" && time.Since(entry.UpdatedAt) > time.Minute {
		r.Status = "interrupted"
	}
	for i, row := range r.Apply.Builds {
		receipt, err := store.DeploymentSubmission(ctx, row.AppID, ProjectWorkloadAttempt(r.AttemptID, row.AppID))
		if errors.Is(err, ErrNotFound) {
			if row.DeploymentID == "" {
				r.Apply.Builds[i].Error = "workload acceptance has not been recorded"
			}
			continue
		}
		if err != nil {
			return r, err
		}
		dep, err := store.DeploymentByID(ctx, receipt.DeploymentID)
		if err != nil {
			return r, err
		}
		if dep.AppID != row.AppID {
			return r, ErrConflict
		}
		r.Apply.Builds[i].DeploymentID, r.Apply.Builds[i].BuildID = dep.ID, dep.BuildID
		if r.Apply.SafeRelease != nil {
			r.Apply.Builds[i].DeploymentStatus = string(dep.Status)
		}
		r.Apply.Builds[i].Error = ""
		if (r.Status == "complete" || r.Status == "interrupted") && dep.BuildID == "" && dep.Kind != DeploymentKindImage && dep.Status != DeployFailed {
			r.Apply.Builds[i].Error = "deployment accepted but build enqueue is unconfirmed; inspect this deployment before retrying"
		}
		if reader, ok := store.(DeploymentConfigurationStore); ok {
			config, err := reader.DeploymentConfiguration(ctx, accountID, dep.ID)
			if err != nil {
				return r, err
			}
			r.Apply.Builds[i].Configuration = &config
		}
	}
	if err := hydrateMigrationContract(ctx, store, accountID, &r); err != nil {
		return r, err
	}
	return r, nil
}
