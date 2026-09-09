package state

import (
	"context"
	"fmt"
)

const builderVMCleanupClaimLease = "5 minutes"

// ClaimBuildVMCleanup claims due or abandoned builder-VM teardown rows. The
// row lock prevents two builderd processes from taking the same live row, and
// the token prevents a late result from an expired claim from completing a
// newer attempt.
func (s *PgStore) ClaimBuildVMCleanup(ctx context.Context, limit int) ([]BuildVMCleanupClaim, error) {
	if limit <= 0 {
		return nil, nil
	}
	rows, err := s.pool.Query(ctx, `
		WITH claimable AS (
			SELECT build_id
			  FROM builder_vm_cleanup
			 WHERE (claimed_at IS NULL AND next_attempt_at <= now())
			    OR (claimed_at IS NOT NULL AND claimed_at < now() - $2::interval)
			 ORDER BY next_attempt_at, build_id
			 FOR UPDATE SKIP LOCKED
			 LIMIT $1
		)
		UPDATE builder_vm_cleanup c
		   SET claimed_at = now(),
		       claim_token = gen_random_uuid(),
		       attempts = c.attempts + 1,
		       updated_at = now()
		  FROM claimable q
		 WHERE c.build_id = q.build_id
		RETURNING c.build_id, c.claim_token`, limit, builderVMCleanupClaimLease)
	if err != nil {
		return nil, fmt.Errorf("ClaimBuildVMCleanup: query: %w", err)
	}
	defer rows.Close()
	claims := make([]BuildVMCleanupClaim, 0, limit)
	for rows.Next() {
		var claim BuildVMCleanupClaim
		if err := rows.Scan(&claim.BuildID, &claim.ClaimToken); err != nil {
			return nil, fmt.Errorf("ClaimBuildVMCleanup: scan: %w", err)
		}
		claims = append(claims, claim)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("ClaimBuildVMCleanup: rows: %w", err)
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
