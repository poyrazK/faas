// ADR-521: source work cannot become claimable before immutable definitions exist.
package apidsource

import (
	"context"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type operationSourceStore struct {
	*state.MemStore
	fail   bool
	queued bool
}

func (s *operationSourceStore) PutOperationDefinition(ctx context.Context, def state.OperationDefinition) (state.OperationDefinition, error) {
	if s.fail {
		return state.OperationDefinition{}, errors.New("definition write unavailable")
	}
	return s.MemStore.PutOperationDefinition(ctx, def)
}
func (s *operationSourceStore) CreateBuildWithID(ctx context.Context, id, depID string, kind state.DeploymentKind, sourceBytes int64, logPath string) (state.Build, error) {
	dep, err := s.DeploymentByID(ctx, depID)
	if err != nil {
		return state.Build{}, err
	}
	app, err := s.AppByID(ctx, dep.AppID)
	if err != nil {
		return state.Build{}, err
	}
	defs, err := s.OperationDefinitionsForDeployment(ctx, app.AccountID, app.ID, depID)
	if err != nil || len(defs) != 1 {
		return state.Build{}, errors.New("build became claimable before definitions")
	}
	s.queued = true
	return s.MemStore.CreateBuildWithID(ctx, id, depID, kind, sourceBytes, logPath)
}

func TestOperationSourceEnqueueBoundary(t *testing.T) {
	for _, mode := range []string{"disabled", "enabled", "definition failure"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("FAAS_STORAGE_BACKEND", "local")
			t.Setenv("FAAS_SPOOL_ROOT", t.TempDir())
			store := &operationSourceStore{MemStore: state.NewMemStore(), fail: mode == "definition failure"}
			app := mustSeedApp(t, store.MemStore)
			source, size := writeHandlerOnlyTarball(t)
			notifier := &recordingNotifier{}
			spec := api.OperationDefinitionSpec{Name: "export", Method: "POST", Path: "/exports", Owner: api.OperationOwnerPlatformTenant, InputSchema: []byte(`true`), OutputSchema: []byte(`true`), ProgressStages: []string{"generating"}}
			result, err := Enqueue(t.Context(), store, notifier, EnqueueParams{AppID: app.ID, Kind: state.DeploymentKindTarball, SourcePath: source, SourceBytes: size, LogSpool: t.TempDir(), Log: quietLogger(), OperationDefinitions: []api.OperationDefinitionSpec{spec}, OperationAdmissionEnabled: mode != "disabled"})
			if mode == "enabled" {
				if err != nil || result.BuildID == "" || !store.queued || notifier.callCount() == 0 {
					t.Fatalf("qualified queue: %+v %v", result, err)
				}
				return
			}
			if err == nil || store.queued || notifier.callCount() != 0 {
				t.Fatalf("closed queue: %+v %v", result, err)
			}
			dep, depErr := store.LatestDeployment(t.Context(), app.ID)
			if mode == "disabled" && !errors.Is(depErr, state.ErrNotFound) {
				t.Fatalf("disabled admission created deployment: %+v %v", dep, depErr)
			}
			if mode == "definition failure" && (depErr != nil || dep.Status != state.DeployFailed) {
				t.Fatalf("failed install stayed active: %+v %v", dep, depErr)
			}
		})
	}
}
