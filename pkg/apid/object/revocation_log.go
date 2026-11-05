package object

import (
	"context"
	"maps"
	"slices"
)

// emitRevocation records a revoked S3 key, copy source, compute binding or
// access grant in the audit log and in apid's own log. The audit log lives
// in the control-plane database, so a restore rolls it back together with
// the revocation; the log line outlives the restore in the log pipeline,
// and docs/runbooks/ObjectStorageRestoreReconcile.md replays revocations
// from it. data holds IDs only.
func (s *Service) emitRevocation(ctx context.Context, kind, accountID string, data map[string]any) {
	s.Host.EmitAudit(ctx, kind, &accountID, data)
	args := []any{"kind", kind, "account_id", accountID}
	for _, key := range slices.Sorted(maps.Keys(data)) {
		args = append(args, key, data[key])
	}
	s.Host.Logger().InfoContext(ctx, "object storage access revoked", args...)
}
