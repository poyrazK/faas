package vmmdgrpc

import (
	"context"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/wire"
)

func devSourceDivergedProblem(instance string) *api.Problem {
	return api.NewProblem(http.StatusConflict, api.CodeDevSourceDiverged,
		"Instance not snapshotted",
		"instance "+instance+" was served a developer live patch, so its source no longer matches the deployment artifact; it was not snapshotted").
		WithDocs(wire.DocsBaseURL + "/vmmd#pause")
}

// destroyDiverged tears a patched instance down the way a successful park
// would, then reports dev_source_diverged so the scheduler records why no
// snapshot exists. The next wake restores the deployment's own snapshot or
// cold-boots its rootfs (ADR-005).
func (s *Server) destroyDiverged(ctx context.Context, instance string) *api.Problem {
	if err := s.vmm.Destroy(ctx, instance); err != nil {
		return toProblem(err)
	}
	s.streamBridges.forget(context.WithoutCancel(ctx), instance)
	s.ForgetCPU(instance)
	s.ForgetNet(instance)
	s.ForgetActivity(instance)
	s.diverged.Forget(instance)
	return devSourceDivergedProblem(instance)
}
