package vmmdgrpc

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// BuilderClaimStore is the durable placement view for queued customer builds.
// Platform probes and development-session builders use separate identities.
type BuilderClaimStore interface {
	BuildByID(context.Context, string) (state.Build, error)
	DeploymentByID(context.Context, string) (state.Deployment, error)
	AppByID(context.Context, string) (state.App, error)
	ComputeNodeByID(context.Context, string) (state.ComputeNode, error)
}

func (s *Server) WithBuilderClaimStore(store BuilderClaimStore) *Server {
	if s != nil {
		s.builderClaimStore = store
	}
	return s
}

// validateBuilderPlacement fences dispatch against a mismatched vmmd target
// and cancelled claims before either cold boot or warm snapshot restore. The
// region comes from durable app intent, never from an RPC locality hint.
func (s *Server) validateBuilderPlacement(ctx context.Context, instance string, isBuilder bool) error {
	if !isBuilder || s.builderClaimStore == nil {
		return nil
	}
	buildID, durable := durableBuilderInstanceID(instance)
	if !durable {
		// Preserve the established non-durable probe/dev-session namespace.
		return nil
	}
	build, err := s.builderClaimStore.BuildByID(ctx, buildID)
	if errors.Is(err, state.ErrNotFound) {
		return status.Error(codes.FailedPrecondition, "builder claim is missing")
	}
	if err != nil {
		return status.Error(codes.Unavailable, "cannot verify builder claim")
	}
	if build.Status != state.BuildRunning {
		return status.Error(codes.FailedPrecondition, "builder claim is no longer running")
	}
	if build.BuilderNodeID != "" && build.BuilderNodeID != s.nodeID {
		return status.Error(codes.FailedPrecondition, "builder claim belongs to another compute node")
	}
	dep, err := s.builderClaimStore.DeploymentByID(ctx, build.DeploymentID)
	if err != nil {
		return status.Error(codes.Unavailable, "cannot verify builder deployment")
	}
	app, err := s.builderClaimStore.AppByID(ctx, dep.AppID)
	if err != nil {
		return status.Error(codes.Unavailable, "cannot verify builder region")
	}
	if app.ComputeRegion == "" {
		return nil
	}
	if build.BuilderNodeID == "" || s.nodeID == "" {
		return status.Error(codes.FailedPrecondition, "regional builder requires a registered claim owner")
	}
	node, err := s.builderClaimStore.ComputeNodeByID(ctx, s.nodeID)
	if err != nil {
		return status.Error(codes.Unavailable, "cannot verify builder compute node")
	}
	if !node.Active || !state.ComputeRegionAllowsNode(app.ComputeRegion, node) {
		return status.Error(codes.FailedPrecondition, "builder compute node is outside the selected region or inactive")
	}
	return nil
}

func durableBuilderInstanceID(instance string) (string, bool) {
	buildID, found := strings.CutPrefix(instance, "build-")
	return buildID, found && uuid.Validate(buildID) == nil
}
