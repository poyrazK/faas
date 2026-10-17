// Package bindingrun coordinates existing durable receipts. Admission and
// qualification remain server-authoritative; stopping a runner never cancels work.
package bindingrun

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// Client is implemented by the API client and the public Go SDK client.
type Client interface {
	ResumeBindingVerificationRun(context.Context, string, string) (api.BindingVerificationRun, error)
	GetBindingVerificationRun(context.Context, string, string) (api.BindingVerificationRun, error)
	AdmitBindingVerificationRunStep(context.Context, string, string, int) (api.BindingVerificationRunStep, error)
	CompleteBindingVerificationRun(context.Context, string, string) (api.BindingVerificationRun, error)
	ResumeProjectBindingVerificationRun(context.Context, string, string, string) (api.ProjectBindingVerificationRun, error)
	GetProjectBindingVerificationRun(context.Context, string, string, string) (api.ProjectBindingVerificationRun, error)
	CompleteProjectBindingVerificationRun(context.Context, string, string, string) (api.ProjectBindingVerificationRun, error)
}

type Options struct {
	// Nil uses three retries per failed status read; a pointer to zero disables retries.
	StatusReadRetries *int
	// SkipDiagnostics disables best-effort read-only failure enrichment.
	SkipDiagnostics bool
	// Zero uses the default. Concurrency counts unfinished resources across all members.
	PollInterval time.Duration
	Concurrency  int
	// Called synchronously with sanitized identity/status metadata. An error stops
	// the runner without cancelling or replacing admitted resources.
	OnProgress func(Progress) error
}
type Progress struct {
	Operation              string                                `json:"operation,omitempty"`
	Qualification          *api.BindingVerificationQualification `json:"qualification,omitempty"`
	RetryAttempt           int                                   `json:"retry_attempt,omitempty"`
	RetryDelayMilliseconds int64                                 `json:"retry_delay_ms,omitempty"`
	RunID                  string                                `json:"run_id"`
	App                    string                                `json:"app,omitempty"`
	MemberRunID            string                                `json:"member_run_id,omitempty"`
	Step                   int                                   `json:"step"` // -1 identifies the root receipt.
	ResourceID             string                                `json:"resource_id,omitempty"`
	ResourceKind           string                                `json:"resource_kind,omitempty"`
	Status                 string                                `json:"status"`
}

// RecoveryError always names the original receipt, including response loss and
// callback failures. Resume that ID; creating a replacement could duplicate work.
type RecoveryError struct {
	RunID              string
	Err                error
	Diagnostics        *api.BindingVerificationRunDiagnostics
	ProjectDiagnostics *api.ProjectBindingVerificationRunDiagnostics
	original           snapshot
	skipDiagnostics    bool
}

func (e *RecoveryError) Error() string {
	return fmt.Sprintf("verification run %s stopped; resume this run: %v", e.RunID, e.Err)
}
func (e *RecoveryError) Unwrap() error { return e.Err }

type snapshot struct {
	app   *api.BindingVerificationRun
	graph *api.ProjectBindingVerificationRun
}

func (s snapshot) id() string {
	if s.app != nil {
		return s.app.ID
	}
	return s.graph.ID
}
func (s snapshot) status() string {
	if s.app != nil {
		return s.app.Status
	}
	return s.graph.Status
}
func (s snapshot) expires() time.Time {
	if s.app != nil {
		return s.app.ExpiresAt
	}
	return s.graph.ExpiresAt
}
func (s snapshot) members() []api.BindingVerificationRun {
	if s.app != nil {
		return []api.BindingVerificationRun{*s.app}
	}
	names := make([]string, 0, len(s.graph.Members))
	for name := range s.graph.Members {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]api.BindingVerificationRun, 0, len(names))
	for _, name := range names {
		out = append(out, s.graph.Members[name])
	}
	return out
}
func (s snapshot) setStep(app string, index int, step api.BindingVerificationRunStep) {
	if s.app != nil {
		s.app.Steps[index] = step
		return
	}
	member := s.graph.Members[app]
	member.Steps[index] = step
	s.graph.Members[app] = member
}

// Detached immutable selections include the original plan and public review,
// while resource references are checked separately as admissions accumulate.
func (s snapshot) immutable() string {
	var value any
	clean := func(r api.BindingVerificationRun) api.BindingVerificationRun {
		r.Status = ""
		r.Reason = ""
		r.Qualification = nil
		r.Steps = append([]api.BindingVerificationRunStep(nil), r.Steps...)
		for i := range r.Steps {
			r.Steps[i].Status = ""
			r.Steps[i].ResourceID = ""
			r.Steps[i].ResourceKind = ""
			r.Steps[i].Owned = false
		}
		return r
	}
	if s.app != nil {
		value = clean(*s.app)
	} else {
		g := *s.graph
		g.Status = ""
		g.Reason = ""
		g.Qualification = nil
		g.Members = maps.Clone(g.Members)
		for name, r := range g.Members {
			g.Members[name] = clean(r)
		}
		value = g
	}
	data, _ := json.Marshal(value)
	return string(data)
}

func RunApp(ctx context.Context, c Client, app, id string, o Options) (api.BindingVerificationRun, error) {
	var out api.BindingVerificationRun
	if err := normalize(&o); err != nil {
		return out, err
	}
	read := func(ctx context.Context, resume bool) (snapshot, error) {
		var out api.BindingVerificationRun
		var err error
		if resume {
			out, err = c.ResumeBindingVerificationRun(ctx, app, id)
		} else {
			out, err = c.GetBindingVerificationRun(ctx, app, id)
		}
		return snapshot{app: &out}, err
	}
	complete := func(ctx context.Context) (snapshot, error) {
		r, err := c.CompleteBindingVerificationRun(ctx, app, id)
		return snapshot{app: &r}, err
	}
	s, err := drive(ctx, c, id, o, read, complete, func(s snapshot) error {
		if s.app == nil || s.app.ID != id || s.app.App != app {
			return errors.New("server returned another app run")
		}
		return validate(s)
	})
	if s.app != nil {
		out = *s.app
	}
	attachAppDiagnostics(ctx, c, app, id, o, err)
	return out, err
}
func RunProject(ctx context.Context, c Client, project, environment, id string, o Options) (api.ProjectBindingVerificationRun, error) {
	var out api.ProjectBindingVerificationRun
	if err := normalize(&o); err != nil {
		return out, err
	}
	read := func(ctx context.Context, resume bool) (snapshot, error) {
		var out api.ProjectBindingVerificationRun
		var err error
		if resume {
			out, err = c.ResumeProjectBindingVerificationRun(ctx, project, environment, id)
		} else {
			out, err = c.GetProjectBindingVerificationRun(ctx, project, environment, id)
		}
		return snapshot{graph: &out}, err
	}
	complete := func(ctx context.Context) (snapshot, error) {
		r, err := c.CompleteProjectBindingVerificationRun(ctx, project, environment, id)
		return snapshot{graph: &r}, err
	}
	s, err := drive(ctx, c, id, o, read, complete, func(s snapshot) error {
		if s.graph == nil || s.graph.ID != id || s.graph.Project != project || s.graph.Environment != environment {
			return errors.New("server returned another project run")
		}
		return validate(s)
	})
	if s.graph != nil {
		out = *s.graph
	}
	attachProjectDiagnostics(ctx, c, project, environment, id, o, err)
	return out, err
}
func normalize(o *Options) error {
	if o.PollInterval == 0 {
		o.PollInterval = time.Second
	}
	if o.Concurrency == 0 {
		o.Concurrency = api.BindingVerificationRunnerDefaultConcurrency
	}
	if o.StatusReadRetries != nil && (*o.StatusReadRetries < 0 || *o.StatusReadRetries > api.BindingVerificationRunnerMaxStatusReadRetries) {
		return errors.New("status read retries must be between zero and eight")
	}
	if o.PollInterval < 0 || o.Concurrency < 1 || o.Concurrency > api.BindingVerificationRunnerMaxConcurrency {
		return errors.New("poll interval must be positive and concurrency must be between 1 and 8")
	}
	return nil
}
func validate(s snapshot) error {
	if s.id() == "" || s.status() != "pending" && s.status() != "passed" {
		return fmt.Errorf("run is not resumable: %s", s.status())
	}
	for _, r := range s.members() {
		if r.MaxConcurrency < 0 || r.MaxConcurrency > api.BindingVerificationRunnerMaxConcurrency || !validID(r.ID) || r.App == "" || !validID(canonicalDeploymentID(r.DeploymentID)) || r.ReviewDigest == "" || r.CreatedAt.IsZero() || r.CreatedAt.After(time.Now()) || !r.ExpiresAt.After(time.Now()) || !r.ExpiresAt.After(r.CreatedAt) || r.ExpiresAt.Sub(r.CreatedAt) > time.Duration(api.BindingVerificationRunMaxTTLSeconds)*time.Second || len(r.Steps) > api.BindingVerificationRunMaxSteps || r.Status != "pending" && r.Status != "passed" {
			return errors.New("server did not confirm a resumable member and original deadline")
		}
	}
	if s.graph != nil {
		g := s.graph
		if g.ReviewDigest == "" || len(g.Members) == 0 || len(g.Members) != len(g.Manifest.Deployments) || len(g.Members) > api.ProjectReleaseSetMaxMembers || g.Manifest.Project != g.Project || g.Manifest.Environment != g.Environment {
			return errors.New("server did not confirm graph membership")
		}
		for name, r := range g.Members {
			if api.BindingVerificationConcurrency(r.MaxConcurrency) != api.BindingVerificationConcurrency(g.MaxConcurrency) || r.App != name || r.GraphRunID != g.ID || canonicalDeploymentID(r.DeploymentID) != canonicalDeploymentID(g.Manifest.Deployments[name]) || r.Scope != g.Environment || !r.CreatedAt.Equal(g.CreatedAt) || !r.ExpiresAt.Equal(g.ExpiresAt) || r.Policy.Revision != g.Manifest.ExpectedPolicyRevisions[name] || !maps.Equal(r.Inventory.ServiceTargets, g.Manifest.ServiceTargets[name]) || r.QueuePublish != g.QueuePublish || r.ObjectStorageWrite != g.ObjectStorageWrite {
				return errors.New("server changed the graph member selection")
			}
		}
	}
	return nil
}

type reference struct {
	id, kind string
	owned    bool
}
type candidate struct {
	app, run string
	index    int
	step     api.BindingVerificationRunStep
}

func stepKey(app string, index int) string { return fmt.Sprintf("%s/%d", app, index) }
func expectedKind(step api.BindingVerificationRunStep) string {
	if step.Plan.Type == api.BindingTypeQueue {
		return "queue"
	}
	return "task"
}
func inspect(s snapshot, known map[string]reference) ([]candidate, int, error) {
	candidates := []candidate{}
	active := 0
	for _, r := range s.members() {
		for i, step := range r.Steps {
			key := stepKey(r.App, i)
			old, seen := known[key]
			if seen && (old.id != step.ResourceID || old.kind != step.ResourceKind || old.owned != step.Owned) {
				return nil, 0, errors.New("server changed an admitted resource identity or owner")
			}
			if step.ResourceID != "" {
				if !validID(step.ResourceID) || step.ResourceKind != expectedKind(step) {
					return nil, 0, errors.New("server returned an invalid resource identity")
				}
				if step.Owned && (step.Plan.Action != "probe" && step.Plan.Action != "queue-canary") || !step.Owned && (step.Plan.Action == "probe" || step.Plan.Action == "queue-canary") {
					return nil, 0, errors.New("server did not confirm the reviewed resource owner")
				}
				known[key] = reference{step.ResourceID, step.ResourceKind, step.Owned}
				switch step.Status {
				case "succeeded":
				case "queued", "restoring", "running":
					if step.ResourceKind != "task" {
						return nil, 0, errors.New("invalid queue resource status")
					}
					active++
				case "published", "dispatching":
					if step.ResourceKind != "queue" {
						return nil, 0, errors.New("invalid task resource status")
					}
					active++
				default:
					return nil, 0, fmt.Errorf("resource %s did not succeed: %s", step.ResourceID, step.Status)
				}
				continue
			}
			if step.Owned || step.ResourceKind != "" {
				return nil, 0, errors.New("incomplete resource identity")
			}
			switch step.Plan.Action {
			case "reuse", "observe", "skip":
			case "probe", "queue-canary":
				if r.Status == "passed" {
					return nil, 0, errors.New("passed member has an unadmitted step")
				}
				candidates = append(candidates, candidate{r.App, r.ID, i, step})
			default:
				return nil, 0, fmt.Errorf("reviewed step cannot be executed: %s", step.Plan.Action)
			}
		}
	}
	return candidates, active, nil
}
func drive(ctx context.Context, c Client, id string, o Options, read func(context.Context, bool) (snapshot, error), complete func(context.Context) (snapshot, error), check func(snapshot) error) (current snapshot, failure error) {
	var original snapshot
	localFailure := false
	defer func() {
		if failure != nil {
			failure = &RecoveryError{RunID: id, Err: failure, original: original, skipDiagnostics: localFailure}
		}
	}()
	if !validID(id) {
		return current, errors.New("run ID must be a nonzero canonical UUID")
	}
	if err := ctx.Err(); err != nil {
		return current, err
	}
	current, failure = read(ctx, true)
	if failure != nil {
		return current, failure
	}
	if err := check(current); err != nil {
		return current, err
	}
	original = diagnosticAnchor(current)
	ctx, cancel := context.WithDeadline(ctx, current.expires())
	defer cancel()
	var waiting *api.BindingVerificationQualification
	confirmed := false
	frozen := current.immutable()
	known := map[string]reference{}
	seen := map[string]Progress{}
	notify := func(s snapshot) error {
		if o.OnProgress == nil {
			return nil
		}
		status := s.status()
		if status == "passed" && !confirmed {
			status = "qualifying"
		}
		if status == "pending" && waiting != nil {
			status = "waiting_for_qualification"
		}
		events := []Progress{{RunID: id, Step: -1, Status: status, Qualification: cloneQualification(waiting)}}
		for _, r := range s.members() {
			for i, step := range r.Steps {
				events = append(events, Progress{RunID: id, App: r.App, MemberRunID: r.ID, Step: i, ResourceID: step.ResourceID, ResourceKind: step.ResourceKind, Status: step.Status})
			}
		}
		for _, event := range events {
			key := stepKey(event.App, event.Step)
			if old, ok := seen[key]; !ok || !reflect.DeepEqual(old, event) {
				if err := o.OnProgress(event); err != nil {
					localFailure = true
					return err
				}
				seen[key] = event
			}
		}
		return ctx.Err()
	}
	// Reconciliation reads only the original receipt; an uncertain write is never replayed.
	reconcile := func(cause error, operation string, target *candidate) (snapshot, error) {
		fail := func(err error) (snapshot, error) { return snapshot{}, errors.Join(cause, err) }
		if err := statusReadContextError(ctx); err != nil {
			return fail(err)
		}
		if !statusReadRetryable(cause) {
			return snapshot{}, cause
		}
		emit := func(event Progress) error {
			if o.OnProgress != nil {
				if err := o.OnProgress(event); err != nil {
					localFailure = true
					return err
				}
				seen[stepKey(event.App, event.Step)] = event
			}
			return statusReadContextError(ctx)
		}
		event := Progress{RunID: id, Step: -1, Status: "reconciling", Operation: operation}
		if target != nil {
			event.App = target.app
			event.MemberRunID = target.run
			event.Step = target.index
		}
		if err := emit(event); err != nil {
			return fail(err)
		}
		fresh, err := readStatusWithRetry(ctx, id, o, read, emit)
		if err != nil {
			return fail(err)
		}
		if err = check(fresh); err != nil {
			return fail(err)
		}
		if fresh.immutable() != frozen {
			return fail(errors.New("reconciliation changed the original review or deadline"))
		}
		pending, active, err := inspect(fresh, known)
		if err != nil {
			return fail(err)
		}
		if target == nil {
			if fresh.status() != "passed" || pendingQualificationOf(fresh) != nil || len(pending) != 0 || active != 0 {
				return fail(errors.New("completion was not confirmed"))
			}
			for _, member := range fresh.members() {
				if member.Status != "passed" || member.Qualification != nil {
					return fail(errors.New("member completion was not confirmed"))
				}
			}
		} else {
			proved := false
			for _, member := range fresh.members() {
				if member.App == target.app && member.ID == target.run && target.index < len(member.Steps) {
					step := member.Steps[target.index]
					proved = step.Owned && validID(step.ResourceID) && step.ResourceKind == expectedKind(target.step) && reflect.DeepEqual(step.Plan, target.step.Plan)
				}
			}
			if !proved {
				return fail(errors.New("admission was not confirmed"))
			}
		}
		return fresh, nil
	}

	for {
		if err := ctx.Err(); err != nil {
			return current, err
		}
		if err := check(current); err != nil {
			return current, err
		}
		if current.immutable() != frozen {
			return current, errors.New("server changed the original review or deadline")
		}
		pending, active, err := inspect(current, known)
		if err != nil {
			return current, err
		}
		if err = notify(current); err != nil {
			return current, err
		}
		if current.status() == "passed" {
			if len(pending) != 0 || active != 0 {
				return current, errors.New("server returned a passed run with unfinished resources")
			}
			if current.graph != nil {
				for _, member := range current.graph.Members {
					if member.Status != "passed" {
						return current, errors.New("server did not confirm passed graph members")
					}
				}
			}
			if confirmed {
				return current, nil
			}
		}
		if len(pending) == 0 && active == 0 {
			completed, err := complete(ctx)
			if err != nil {
				completed, err = reconcile(err, "completion", nil)
				if err != nil {
					return current, err
				}
			}
			if completed.status() == "pending" {
				if err = check(completed); err != nil {
					return current, err
				}
				if completed.immutable() != frozen {
					return current, errors.New("server changed the original review during qualification")
				}
				if err = validatePendingQualification(completed); err != nil {
					return current, err
				}
				if pending, count, inspectErr := inspect(completed, known); inspectErr != nil || count != 0 || len(pending) != 0 {
					if inspectErr != nil {
						return current, inspectErr
					}
					return current, errors.New("pending qualification has unfinished resources")
				}
				current = completed
				waiting = cloneQualification(pendingQualificationOf(completed))
				if err = notify(current); err != nil {
					return current, err
				}
				if err = waitQualification(ctx, o.PollInterval); err != nil {
					return current, err
				}
				fresh, readErr := readStatusWithRetry(ctx, id, o, read, func(event Progress) error {
					if o.OnProgress != nil {
						if e := o.OnProgress(event); e != nil {
							localFailure = true
							return e
						}
						seen[stepKey("", -1)] = event
					}
					return ctx.Err()
				})
				if readErr != nil {
					return current, readErr
				}
				current = fresh
				continue
			}
			if completed.status() != "passed" || pendingQualificationOf(completed) != nil {
				return current, errors.New("server did not confirm a passed run for the original review")
			}
			if err = check(completed); err != nil {
				return current, err
			}
			if completed.immutable() != frozen {
				return current, errors.New("server changed the original review at completion")
			}
			if _, count, err := inspect(completed, known); err != nil || count != 0 {
				if err == nil {
					err = errors.New("completed run has unfinished resources")
				}
				return current, err
			}
			current = completed
			waiting = nil
			confirmed = true
			continue
		}
		admitted, budgetFull, reconciled := false, false, false
		for _, next := range pending {
			if active >= o.Concurrency {
				break
			}
			if err = ctx.Err(); err != nil {
				return current, err
			}
			step, err := c.AdmitBindingVerificationRunStep(ctx, next.app, next.run, next.index)
			if budgetBackpressure(err) {
				budgetFull = true
				break
			}
			if err != nil {
				fresh, recoveryErr := reconcile(err, "admission", &next)
				if recoveryErr != nil {
					return current, recoveryErr
				}
				current = fresh
				reconciled = true
				break
			}
			if !reflect.DeepEqual(step.Plan, next.step.Plan) || !step.Owned || step.ResourceKind != expectedKind(next.step) || !validID(step.ResourceID) {
				return current, errors.New("server did not confirm the reviewed admission")
			}
			current.setStep(next.app, next.index, step)
			if _, _, err = inspect(current, known); err != nil {
				return current, err
			}
			if err = notify(current); err != nil {
				return current, err
			}
			if step.Status != "succeeded" {
				active++
			}
			admitted = true
		}
		if reconciled {
			continue
		}
		if !admitted || budgetFull {
			timer := time.NewTimer(o.PollInterval)
			select {
			case <-ctx.Done():
				timer.Stop()
				return current, ctx.Err()
			case <-timer.C:
			}
		}
		fresh, err := readStatusWithRetry(ctx, id, o, read, func(event Progress) error {
			if o.OnProgress != nil {
				if callbackErr := o.OnProgress(event); callbackErr != nil {
					localFailure = true
					return callbackErr
				}
				seen[stepKey("", -1)] = event
			}
			return ctx.Err()
		})
		if err != nil {
			return current, err
		}
		current = fresh
	}
}
func validID(id string) bool {
	if len(id) != 36 || id[8] != '-' || id[13] != '-' || id[18] != '-' || id[23] != '-' || id == "00000000-0000-0000-0000-000000000000" {
		return false
	}
	for i, ch := range id {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			continue
		}
		if (ch < '0' || ch > '9') && (ch < 'a' || ch > 'f') {
			return false
		}
	}
	return true
}

// Deployment state predates UUID-typed receipts and may return compact UUIDs.
func canonicalDeploymentID(id string) string {
	if len(id) == 32 {
		return id[:8] + "-" + id[8:12] + "-" + id[12:16] + "-" + id[16:20] + "-" + id[20:]
	}
	return id
}

// Wire APIError is a carrier, and may unwrap to a sentinel rather than Problem.
func budgetBackpressure(err error) bool {
	problem := api.AsProblem(err)
	if problem == nil {
		var wire *api.APIError
		if errors.As(err, &wire) {
			problem = &wire.Problem
		}
	}
	return problem != nil && problem.Status == 429 && problem.Code == "binding_verification_budget_full"
}
