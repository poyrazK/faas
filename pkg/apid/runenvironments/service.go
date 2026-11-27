package runenvironments

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runenv"
	"github.com/onebox-faas/faas/pkg/state"
	"net/http"
	"time"
)

type Access struct {
	Account   state.Account
	Principal string
	Wide      bool
}
type Service struct {
	Store   state.ExecutionEnvironmentStore
	Bases   map[api.ExecutionRuntime]string
	Enabled bool
}

func (s Service) Create(ctx context.Context, a Access, r api.CreateExecutionEnvironmentRequest) (api.ExecutionEnvironmentResponse, error) {
	if !s.Enabled {
		return api.ExecutionEnvironmentResponse{}, api.ErrCapacity("new Run environment builds are disabled")
	}
	if !a.Account.Plan.ExecutionsAllowed() {
		return api.ExecutionEnvironmentResponse{}, api.ErrExecutionsNotAllowed(a.Account.Plan)
	}
	if s.Store == nil {
		return api.ExecutionEnvironmentResponse{}, api.ErrCapacity("Run environment storage unavailable")
	}
	if problem := systemPackageLimits(r.SystemPackages); problem != nil {
		return api.ExecutionEnvironmentResponse{}, problem
	}
	if err := runenv.Validate(r); err != nil {
		return api.ExecutionEnvironmentResponse{}, api.NewProblem(http.StatusUnprocessableEntity, "execution_environment_recipe_invalid", "Invalid dependency recipe", "use exact hash-pinned Python requirements, a public npm lockfile v3, or exact Wolfi system package pins")
	}
	if !runenv.ValidBaseRef(s.Bases[r.Runtime]) {
		return api.ExecutionEnvironmentResponse{}, api.ErrCapacity("dependency environment installer is not configured for this runtime")
	}
	r = runenv.Canonical(r)
	digest, err := runenv.Digest(r, s.Bases[r.Runtime])
	if err != nil {
		return api.ExecutionEnvironmentResponse{}, api.ErrValidation("use a configured runtime with hash-pinned Python requirements or npm lockfile v3 from the public registry")
	}
	row, err := s.Store.CreateExecutionEnvironment(ctx, state.ExecutionEnvironment{ID: uuid.NewString(), AccountID: a.Account.ID, PrincipalID: a.Principal, Runtime: r.Runtime, Recipe: r, RecipeDigest: digest, BaseRef: s.Bases[r.Runtime]})
	return Response(row), err
}

func systemPackageLimits(pins []string) *api.Problem {
	problem := func(detail string) *api.Problem {
		return api.NewProblem(http.StatusUnprocessableEntity, "execution_environment_recipe_invalid", "Invalid dependency recipe", detail).WithDocs("https://gregale.dev/docs/executions#system-package-environments")
	}
	if len(pins) > api.ExecutionEnvironmentMaxSystemPackages {
		return problem("too many system package pins").WithLimit(api.ExecutionEnvironmentMaxSystemPackages, int64(len(pins)))
	}
	for _, pin := range pins {
		if len(pin) > api.ExecutionEnvironmentSystemPackageMaxBytes {
			return problem("system package pin exceeds the byte allowance").WithByteLimit(api.ExecutionEnvironmentSystemPackageMaxBytes, int64(len(pin)))
		}
	}
	return nil
}
func (s Service) Get(ctx context.Context, a Access, id string) (api.ExecutionEnvironmentResponse, error) {
	if s.Store == nil {
		return api.ExecutionEnvironmentResponse{}, api.ErrCapacity("Run environment storage unavailable")
	}
	row, err := s.Store.ExecutionEnvironmentByID(ctx, a.Account.ID, id)
	if err == nil && !a.Wide && row.PrincipalID != a.Principal {
		err = state.ErrNotFound
	}
	return Response(row), err
}
func (s Service) List(ctx context.Context, a Access) ([]api.ExecutionEnvironmentResponse, error) {
	if s.Store == nil {
		return nil, api.ErrCapacity("Run environment storage unavailable")
	}
	rows, err := s.Store.ListExecutionEnvironments(ctx, a.Account.ID, a.Principal, a.Wide)
	out := make([]api.ExecutionEnvironmentResponse, 0, len(rows))
	for _, row := range rows {
		out = append(out, Response(row))
	}
	return out, err
}
func (s Service) Retire(ctx context.Context, a Access, id string) error {
	if s.Store == nil {
		return api.ErrCapacity("Run environment storage unavailable")
	}
	return s.Store.RetireExecutionEnvironment(ctx, a.Account.ID, id, a.Principal, a.Wide)
}
func Response(row state.ExecutionEnvironment) api.ExecutionEnvironmentResponse {
	return api.ExecutionEnvironmentResponse{ID: row.ID, Runtime: row.Runtime, Status: runenv.PublicStatus(row.Status), RecipeDigest: row.RecipeDigest, ImageDigest: row.ImageDigest, ArtifactBytes: row.ArtifactBytes, FailureCode: row.FailureCode, CreatedAt: row.CreatedAt.UTC().Format(time.RFC3339Nano)}
}
func Problem(err error) *api.Problem {
	var p *api.Problem
	if errors.As(err, &p) {
		return p
	}
	if errors.Is(err, state.ErrNotFound) || errors.Is(err, state.ErrExecutionEnvironmentUnavailable) {
		return api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Run environment unavailable", "no ready Run environment is available to this principal")
	}
	var quota *state.ExecutionEnvironmentQuotaError
	if errors.As(err, &quota) {
		return api.NewProblem(http.StatusTooManyRequests, "execution_environment_quota", "Run environment quota exceeded", "retire an environment and wait for physical deletion before building another").WithLimit(int64(quota.Limit), int64(quota.Observed)).WithDocs("https://gregale.dev/docs/executions#dependency-environments")
	}
	return api.ErrCapacity("Run environment service unavailable")
}

// Events returns a scoped retained tail. A cursor never silently skips evicted events.
func (s Service) Events(ctx context.Context, a Access, id string, after int64) (api.ExecutionEnvironmentEventsResponse, error) {
	if s.Store == nil {
		return api.ExecutionEnvironmentEventsResponse{}, api.ErrCapacity("Run environment storage unavailable")
	}
	stored, err := s.Store.ExecutionEnvironmentByID(ctx, a.Account.ID, id)
	if err != nil {
		return api.ExecutionEnvironmentEventsResponse{}, err
	}
	if !a.Wide && stored.PrincipalID != a.Principal {
		return api.ExecutionEnvironmentEventsResponse{}, state.ErrNotFound
	}
	if after < 0 || after > stored.DiagnosticSequence {
		return api.ExecutionEnvironmentEventsResponse{}, api.ErrValidation("after must be a retained or earlier diagnostic sequence")
	}
	out := api.ExecutionEnvironmentEventsResponse{Events: []api.ExecutionEnvironmentEvent{}, Status: runenv.PublicStatus(stored.Status), NextSequence: after}
	for _, e := range stored.Diagnostics {
		if e.Sequence > after {
			out.Events = append(out.Events, e)
			out.NextSequence = e.Sequence
		}
	}
	if len(out.Events) > 0 {
		out.Gap = out.Events[0].Sequence > after+1
	}
	return out, nil
}
