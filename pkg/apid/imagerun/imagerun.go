// Package imagerun serves POST /v1/image-runs (ADR-1161): `docker run`-shaped
// one-task Job runs against a platform-managed runner job per account and
// image. The runner job caches the materialized image; runs, logs and
// cancellation use the ordinary /v1/jobs/{job}/runs routes.
package imagerun

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apptaskattach"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/oci"
	"github.com/onebox-faas/faas/pkg/state"
)

// EnvGate is the apid operator gate for the route.
const EnvGate = "FAAS_IMAGE_RUNS"

const requestMaxBytes = 256 << 10

// EnabledFromEnv reports whether the operator enabled image runs.
func EnabledFromEnv(getenv func(string) string) bool {
	return getenv(EnvGate) == "1"
}

// Store is the state the handler needs.
type Store interface {
	state.ImageRunJobStore
	JobRunCreate(ctx context.Context, jobID, accountID, triggerKind string, parallelism, retryMaxOverride, taskTimeoutOverride *int, envOverrides json.RawMessage, tasks int, options ...state.JobRunOptions) (state.JobRun, []state.JobTask, error)
}

// InteractiveEnvGate is the apid operator gate for interactive image runs
// (ADR-1330), on top of EnvGate.
const InteractiveEnvGate = "FAAS_INTERACTIVE_IMAGE_RUNS"

// InteractiveEnabledFromEnv reports whether the operator enabled
// interactive image runs.
func InteractiveEnabledFromEnv(getenv func(string) string) bool {
	return getenv(InteractiveEnvGate) == "1"
}

// Handler serves POST /v1/image-runs.
type Handler struct {
	Enabled bool
	// InteractiveEnabled admits `interactive: true` (ADR-1330).
	InteractiveEnabled bool
	// Store is nil when the configured store cannot hold runner jobs.
	Store  Store
	Audit  func(ctx context.Context, kind string, accountID *string, data map[string]any)
	Notify func(ctx context.Context, channel, payload string) error
	Log    *slog.Logger
}

// request is a validated CreateImageRunRequest.
type request struct {
	ensure state.ImageRunEnsure
	env    json.RawMessage
	raw    api.CreateImageRunRequest
}

// Create handles POST /v1/image-runs.
func (h *Handler) Create(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !h.Enabled || h.Store == nil {
		httpkit.NotFound(w, "image runs are not enabled")
		return
	}
	if !acct.Plan.JobsAllowed() {
		api.WriteProblem(w, api.ErrPlanJobsNotAllowed(acct.Plan))
		return
	}
	var body api.CreateImageRunRequest
	if !httpkit.DecodeJSONLimit(w, r, &body, requestMaxBytes) {
		return
	}
	if body.Interactive && !h.InteractiveEnabled {
		api.WriteProblem(w, api.NewProblem(http.StatusNotImplemented, api.CodeNotImplemented,
			"Interactive runs unavailable", "interactive image runs are not enabled on this control-plane host"))
		return
	}
	req, prob := resolve(acct, body)
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	res, err := h.Store.JobEnsureImageRun(r.Context(), req.ensure)
	if err != nil {
		api.WriteProblem(w, h.ensureProblem(acct, req.ensure.Name, err))
		return
	}
	h.announce(r.Context(), acct, res)
	if res.Job.ImageMaterializationStatus != "ready" {
		httpkit.WriteJSON(w, http.StatusAccepted, response(res.Job, api.ImageRunStatusPulling))
		return
	}
	h.startRun(w, r, acct, req, res.Job)
}

func (h *Handler) startRun(w http.ResponseWriter, r *http.Request, acct state.Account, req request, job state.Job) {
	argv := api.ResolveImageRunCommand(job.ImageEntrypoint, job.ImageCmd, req.raw.Entrypoint, req.raw.Args)
	if len(argv) == 0 || argv[0] == "" {
		api.WriteProblem(w, api.ErrImageRunCommandRequired(job.ImageRef))
		return
	}
	if len(argv) > api.ImageRunArgvMax {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid command", fmt.Sprintf("the resolved command has %d entries; at most %d are allowed", len(argv), api.ImageRunArgvMax)))
		return
	}
	opts := state.JobRunOptions{CommandArgs: &argv}
	token := ""
	if req.raw.Interactive {
		var prob *api.Problem
		if token, prob = apptaskattach.Mint(true); prob != nil {
			api.WriteProblem(w, prob)
			return
		}
		digest := sha256.Sum256([]byte(token))
		opts.Attach = &state.JobRunAttach{TTY: req.raw.TTY, TokenSHA256: digest[:], AttachTimeoutSec: api.ImageRunAttachTimeoutSec}
	}
	one, zero, timeout := 1, 0, req.ensure.TaskTimeoutSec
	run, _, err := h.Store.JobRunCreate(r.Context(), job.ID, acct.ID, "manual", &one, &zero, &timeout,
		req.env, 1, opts)
	if err != nil {
		if errors.Is(err, state.ErrConflict) {
			api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeValidation,
				"Image unavailable", "the image could not be prepared; run again to retry the pull"))
			return
		}
		api.WriteProblem(w, httpkit.CustomerCapacityProblem(h.Log, "image_run.create_run", "Could not start the run",
			"the run could not be created", "retry the request", err))
		return
	}
	h.Audit(r.Context(), "image_run.created", &acct.ID, map[string]any{"job_id": job.ID, "run_id": run.ID, "image": job.ImageRef,
		"interactive": req.raw.Interactive})
	h.notify(r.Context(), fmt.Sprintf(`{"kind":"run_created","job_id":"%s","run_id":"%s","account_id":"%s"}`, job.ID, run.ID, acct.ID))
	resp := response(job, api.ImageRunStatusQueued)
	resp.RunID, resp.Command = run.ID, argv
	if token != "" {
		// The token is shown once, so the response must never be cached or
		// stored for Idempotency-Key replay.
		w.Header().Set("Cache-Control", "no-store")
		httpkit.SkipIdempotentReplay(w)
		resp.Attach = &api.AppTaskAttachInfo{Token: token, Path: api.JobTaskAttachPath(job.Name, run.ID, 0),
			Subprotocol: api.AppTaskAttachSubprotocol}
	}
	httpkit.WriteJSON(w, http.StatusCreated, resp)
}

// announce tells imaged about pending pulls and evictions; both handlers
// are idempotent, and imaged's sweeps repair a lost notification.
func (h *Handler) announce(ctx context.Context, acct state.Account, res state.ImageRunEnsureResult) {
	for _, job := range res.Evicted {
		h.Audit(ctx, "image_run.evicted", &acct.ID, map[string]any{"job_id": job.ID, "image": job.ImageRef})
		h.notify(ctx, fmt.Sprintf(`{"kind":"deleted","job_id":"%s","account_id":"%s"}`, job.ID, acct.ID))
	}
	if res.Job.ImageMaterializationStatus != "ready" {
		h.notify(ctx, fmt.Sprintf(`{"kind":"image_run","job_id":"%s","account_id":"%s"}`, res.Job.ID, acct.ID))
	}
}

func (h *Handler) notify(ctx context.Context, payload string) {
	if err := h.Notify(ctx, db.NotifyJobChanged, payload); err != nil {
		h.Log.Warn("image run: notify job change", "err", err)
	}
}

func (h *Handler) ensureProblem(acct state.Account, name string, err error) *api.Problem {
	var full *state.ImageRunCacheFullError
	switch {
	case errors.As(err, &full):
		return api.ErrImageRunCacheFull(acct.Plan, full.Limit, full.Observed)
	case errors.Is(err, state.ErrConflict):
		return api.ErrImageRunManaged(name)
	default:
		return httpkit.CustomerCapacityProblem(h.Log, "image_run.ensure", "Could not prepare the image run",
			"the runner job could not be created", "retry the request", err)
	}
}

func response(job state.Job, status string) api.ImageRunResponse {
	return api.ImageRunResponse{
		Status: status, Job: job.Name, Image: job.ImageRef,
		ImageStatus: job.ImageMaterializationStatus, ImageError: job.ImageMaterializationError,
		ImageDigest: job.ImageResolvedDigest,
	}
}

// RunnerName derives the runner job name from the normalized image
// reference. The name fits the jobs slug constraint.
func RunnerName(normalizedImage string) string {
	sum := sha256.Sum256([]byte(normalizedImage))
	return "ir-" + hex.EncodeToString(sum[:8])
}

func resolve(acct state.Account, body api.CreateImageRunRequest) (request, *api.Problem) {
	image := strings.TrimSpace(body.Image)
	if image == "" || len(image) > api.ImageRunImageMaxBytes {
		return request{}, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid image", fmt.Sprintf("image is required and must be at most %d bytes", api.ImageRunImageMaxBytes))
	}
	ref, err := oci.ParseReference(image)
	if err != nil {
		return request{}, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Invalid image", err.Error())
	}
	pull := body.Pull
	if pull == "" {
		pull = api.ImageRunPullMissing
	}
	if pull != api.ImageRunPullMissing && pull != api.ImageRunPullAlways {
		return request{}, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid pull", `pull must be "missing" or "always"`)
	}
	if prob := validateArgv("entrypoint", body.Entrypoint); prob != nil {
		return request{}, prob
	}
	if prob := validateArgv("args", body.Args); prob != nil {
		return request{}, prob
	}
	ramMB, timeoutSec, prob := resources(acct.Plan, body.RAMMB, body.TimeoutSec)
	if prob != nil {
		return request{}, prob
	}
	if prob := validateInteractive(body, timeoutSec); prob != nil {
		return request{}, prob
	}
	env, prob := encodeEnv(acct.Plan, body.Env)
	if prob != nil {
		return request{}, prob
	}
	normalized := ref.String()
	return request{
		ensure: state.ImageRunEnsure{
			AccountID: acct.ID, Name: RunnerName(normalized), ImageRef: normalized,
			RAMMB: ramMB, TaskTimeoutSec: timeoutSec, PullAlways: pull == api.ImageRunPullAlways,
			MaxCached: api.ImageRunCachedImages[acct.Plan.PlanIndex()],
		},
		env: env,
		raw: body,
	}, nil
}

func validateArgv(field string, argv *[]string) *api.Problem {
	if argv == nil {
		return nil
	}
	if len(*argv) > api.ImageRunArgvMax {
		return api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid "+field, fmt.Sprintf("%s may contain at most %d entries", field, api.ImageRunArgvMax))
	}
	for _, arg := range *argv {
		if strings.ContainsRune(arg, 0) || !utf8.ValidString(arg) {
			return api.NewProblem(http.StatusBadRequest, api.CodeValidation,
				"Invalid "+field, field+" entries must be UTF-8 without NUL bytes")
		}
	}
	return nil
}

// validateInteractive applies ADR-1330: a tty needs an interactive run, and
// the task timeout is the session limit.
func validateInteractive(body api.CreateImageRunRequest, timeoutSec int) *api.Problem {
	if body.TTY && !body.Interactive {
		return api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid tty", "tty requires interactive")
	}
	if body.Interactive && timeoutSec > api.ImageRunInteractiveMaxTimeoutSec {
		return api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Invalid timeout",
			fmt.Sprintf("an interactive run's timeout_sec is at most %d", api.ImageRunInteractiveMaxTimeoutSec))
	}
	return nil
}

// resources applies the per-plan Job ceilings. Like `docker run`, an image
// run has no short default deadline: timeout 0 means the plan's ceiling.
func resources(plan api.Plan, ramMB, timeoutSec int) (int, int, *api.Problem) {
	idx := plan.PlanIndex()
	if ramMB == 0 {
		ramMB = api.JobDefaultRAMMB
	}
	if timeoutSec == 0 {
		timeoutSec = api.JobTaskTimeoutSec[idx]
	}
	if ramMB < 0 || timeoutSec < 0 {
		return 0, 0, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid resources", "ram_mb and timeout_sec must be positive")
	}
	if limit := api.JobRAMMB[idx]; ramMB > limit {
		return 0, 0, api.ErrJobQuota(plan, "ram_mb", limit, ramMB)
	}
	if limit := api.JobTaskTimeoutSec[idx]; timeoutSec > limit {
		return 0, 0, api.ErrJobQuota(plan, "timeout_sec", limit, timeoutSec)
	}
	return ramMB, timeoutSec, nil
}

// encodeEnv validates the per-run environment with the Job run rules.
func encodeEnv(plan api.Plan, env map[string]string) (json.RawMessage, *api.Problem) {
	if len(env) == 0 {
		return nil, nil
	}
	limits, _ := api.LimitsFor(plan)
	if len(env) > limits.EnvVarsMax {
		return nil, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid env", "too many per-run environment variables")
	}
	for key, value := range env {
		if prob := api.ValidateEnvKey(key); prob != nil {
			return nil, prob
		}
		if strings.HasPrefix(key, "GREGALE_") || !utf8.ValidString(value) || strings.ContainsRune(value, 0) || len(value) > limits.EnvValueMaxBytes {
			return nil, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
				"Invalid env", "run environment variables must use customer keys and fit the plan value limit")
		}
	}
	encoded, err := json.Marshal(env)
	if err != nil {
		return nil, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Invalid env", err.Error())
	}
	return encoded, nil
}
