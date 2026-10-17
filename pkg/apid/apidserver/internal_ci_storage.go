package apidserver

import (
	"context"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/ciruntime"
	"github.com/onebox-faas/faas/pkg/ci"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) WithInternalCIStorage(blobs *ci.ManagedBlobs, origin string) *server {
	s.ciBlobs, s.ciRuntimeOrigin = blobs, origin
	return s
}

func (s *server) configureInternalCIStorage(ctx context.Context, getenv func(string) string) (func(), error) {
	s.ciRuntimeOrigin = getenv(ci.RuntimeURLEnv)
	if !s.ciConfig.TransportEnabled && !s.ciConfig.RetentionEnabled && !s.ciConfig.WorkerEnabled {
		return func() {}, nil
	}
	if s.ciConfig.TransportEnabled && !ci.ValidRuntimeOrigin(getenv(ci.RuntimeURLEnv)) {
		return nil, ci.ErrStorageUnavailable
	}
	provider, ok := s.store.(interface{ CIStore() ci.Store })
	if !ok {
		return nil, ci.ErrStorageUnavailable
	}
	store, ok := provider.CIStore().(*ci.PostgresStore)
	if !ok {
		return nil, ci.ErrStorageUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(api.CIJobsTransferSeconds)*time.Second)
	defer cancel()
	physical, err := ci.NewGCSObjects(ctx, getenv(ci.StorageBucketEnv))
	if err != nil {
		return nil, err
	}
	s.WithInternalCIStorage(&ci.ManagedBlobs{Store: store, Physical: physical}, getenv(ci.RuntimeURLEnv))
	return func() { _ = physical.Close() }, nil
}

func (s *server) registerInternalCIRuntime(mux *http.ServeMux) {
	handler := ciruntime.Handler{Config: s.ciConfig}
	ready := s.ciBlobs != nil && s.ciBlobs.Store != nil && s.ciBlobs.Physical != nil
	if ready {
		handler.Store, handler.Blobs = s.ciBlobs.Store, s.ciBlobs
	}
	enabled := s.ciConfig.TransportEnabled && ready
	for pattern, action := range map[string]http.HandlerFunc{
		"GET /v1/internal/ci/runtime/{run}/control":        handler.Control,
		"GET /v1/internal/ci/runtime/{run}/source":         handler.Source,
		"PUT /v1/internal/ci/runtime/{run}/objects":        handler.Upload,
		"PUT /v1/internal/ci/runtime/{run}/result":         handler.Seal,
		"PUT /v1/internal/ci/runtime/{run}/progress":       handler.Progress,
		"GET /v1/internal/ci/runtime/{run}/inputs/{input}": handler.Input,
	} {
		mux.HandleFunc(pattern, ciruntime.SecureTransport(s.ciRuntimeOrigin, enabled, action))
	}
	download := s.authLimited(s.internalCIGate(s.requireMFA(s.requireScope(api.ScopesAdminOnly...)(func(w http.ResponseWriter, r *http.Request, account state.Account) {
		handler.Download(w, r, account.ID)
	}))))
	mux.HandleFunc("GET /v1/internal/ci/executions/{execution}/jobs/{job}/artifacts/{artifact...}", ciruntime.SecureTransport(s.ciRuntimeOrigin, enabled, download))
}
