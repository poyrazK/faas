package state

// ADR-20261122220000 amendment 2: before imaged asks secretd for the app
// secrets a verification contract's headers reference, it records itself as
// the node verifying the deployment. secretd opens them only for that node
// while the lease is unexpired.

import (
	"context"
	"fmt"
	"time"
)

// VerificationLeaseStore writes deployment verification leases.
type VerificationLeaseStore interface {
	// PutDeploymentVerificationLease records nodeName as the node verifying
	// deploymentID, a deployment of appID, until expiresAt. A later call
	// replaces the row: the latest activation owns the verification.
	PutDeploymentVerificationLease(ctx context.Context, deploymentID, appID, nodeName string, expiresAt time.Time) error
	// DeleteDeploymentVerificationLease ends the lease when verification
	// finishes; deleting a missing row is not an error.
	DeleteDeploymentVerificationLease(ctx context.Context, deploymentID string) error
}

// VerificationLease is one lease row (MemStore only).
type VerificationLease struct {
	DeploymentID, AppID, NodeName string
	ExpiresAt                     time.Time
}

func (s *PgStore) PutDeploymentVerificationLease(ctx context.Context, deploymentID, appID, nodeName string, expiresAt time.Time) error {
	_, err := s.pool.Exec(ctx, `
INSERT INTO deployment_verification_leases (deployment_id, app_id, node_name, expires_at)
VALUES ($1, $2, $3, $4)
ON CONFLICT (deployment_id) DO UPDATE
   SET app_id = EXCLUDED.app_id, node_name = EXCLUDED.node_name, expires_at = EXCLUDED.expires_at`,
		deploymentID, appID, nodeName, expiresAt)
	if err != nil {
		return fmt.Errorf("state: put deployment verification lease: %w", err)
	}
	return nil
}

func (s *PgStore) DeleteDeploymentVerificationLease(ctx context.Context, deploymentID string) error {
	if _, err := s.pool.Exec(ctx, `DELETE FROM deployment_verification_leases WHERE deployment_id = $1`, deploymentID); err != nil {
		return fmt.Errorf("state: delete deployment verification lease: %w", err)
	}
	return nil
}

func (m *MemStore) PutDeploymentVerificationLease(_ context.Context, deploymentID, appID, nodeName string, expiresAt time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.verificationLeases == nil {
		m.verificationLeases = map[string]VerificationLease{}
	}
	m.verificationLeases[deploymentID] = VerificationLease{DeploymentID: deploymentID, AppID: appID, NodeName: nodeName, ExpiresAt: expiresAt}
	return nil
}

func (m *MemStore) DeleteDeploymentVerificationLease(_ context.Context, deploymentID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.verificationLeases, deploymentID)
	return nil
}

// DeploymentVerificationLease returns the MemStore's lease (tests).
func (m *MemStore) DeploymentVerificationLease(deploymentID string) (VerificationLease, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	lease, ok := m.verificationLeases[deploymentID]
	return lease, ok
}

var (
	_ VerificationLeaseStore = (*PgStore)(nil)
	_ VerificationLeaseStore = (*MemStore)(nil)
)
