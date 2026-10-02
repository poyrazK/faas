package state

import (
	"context"
	"fmt"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// ClaimBuildVMCleanup retains the unidentified legacy cleanup surface. It
// cannot consume an obligation belonging to a named builder or regional app.
func (s *PgStore) ClaimBuildVMCleanup(ctx context.Context, limit int) ([]BuildVMCleanupClaim, error) {
	return s.ClaimBuildVMCleanupOnNode(ctx, "", limit)
}

// ClaimBuildVMCleanupOnNode matches the persisted canonical owner, including
// after node inactivity or a region admission pause. Cleanup must still run.
func (s *PgStore) ClaimBuildVMCleanupOnNode(ctx context.Context, nodeName string, limit int) ([]BuildVMCleanupClaim, error) {
	if limit <= 0 {
		return nil, nil
	}
	rows, err := sqlc.New().ClaimBuildVMCleanupOnNode(ctx, s.pool, sqlc.ClaimBuildVMCleanupOnNodeParams{
		NodeName: nodeName, BatchSize: int32(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("state: claim builder VM cleanup on node: %w", err)
	}
	claims := make([]BuildVMCleanupClaim, 0, len(rows))
	for _, row := range rows {
		claims = append(claims, BuildVMCleanupClaim{BuildID: uuidString(row.BuildID), ClaimToken: uuidString(row.ClaimToken)})
	}
	return claims, nil
}

// CompleteBuildVMCleanup removes a successfully stopped VM obligation. A
// failed stop clears the claim and makes the row immediately eligible for the
// next reaper pass. A stale worker result is harmless because the token guard
// makes its UPDATE/DELETE affect zero rows.
func (s *PgStore) CompleteBuildVMCleanup(ctx context.Context, buildID, claimToken string, cleanupErr error) error {
	if cleanupErr == nil {
		if _, err := s.pool.Exec(ctx, `
			DELETE FROM builder_vm_cleanup
			 WHERE build_id = $1::uuid AND claim_token = $2::uuid`, buildID, claimToken); err != nil {
			return fmt.Errorf("CompleteBuildVMCleanup: delete: %w", err)
		}
		return nil
	}
	if _, err := s.pool.Exec(ctx, `
		UPDATE builder_vm_cleanup
		   SET claimed_at = NULL,
		       claim_token = NULL,
		       next_attempt_at = now(),
		       last_error = left($3, 4096),
		       updated_at = now()
		 WHERE build_id = $1::uuid AND claim_token = $2::uuid`, buildID, claimToken, cleanupErr.Error()); err != nil {
		return fmt.Errorf("CompleteBuildVMCleanup: retry: %w", err)
	}
	return nil
}
