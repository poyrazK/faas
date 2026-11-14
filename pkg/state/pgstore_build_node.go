package state

import (
	"context"
	"fmt"
)

// BuildNodeRecorder records which node runs a claimed build
// (ADR-20261110210000 slice 3j-3). secretd admits only that node to pull the
// build app's private base images for its builder VM.
//
// A per-node database login (slice 1c) has its node recorded by the claim
// itself, through the builds_builder_node_scope trigger, and cannot change
// it. A shared login's claim records none, so builderd calls this right
// after claiming; it sets the node only on a running build that has none.
type BuildNodeRecorder interface {
	RecordBuildNode(ctx context.Context, buildID, nodeName string) error
}

// RecordBuildNode implements BuildNodeRecorder.
func (s *PgStore) RecordBuildNode(ctx context.Context, buildID, nodeName string) error {
	if _, err := s.pool.Exec(ctx,
		`update builds set builder_node_name = $2
		  where id = $1 and status = 'running' and builder_node_name is null`,
		buildID, nodeName); err != nil {
		return fmt.Errorf("state: record build %s node: %w", buildID, err)
	}
	return nil
}
