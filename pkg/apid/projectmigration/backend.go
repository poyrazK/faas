// Package projectmigration journals source-backed HTTP/GraphQL migrations.
// Reconcile, secret sealing and deployment admission remain with their owners.
package projectmigration

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidsource"
	"github.com/onebox-faas/faas/pkg/state"
)

type Reader interface {
	AppByID(context.Context, string) (state.App, error)
	DeploymentByID(context.Context, string) (state.Deployment, error)
	BuildByID(context.Context, string) (state.Build, error)
	LatestDeploymentForScope(context.Context, string, string) (state.Deployment, error)
}
type Backend struct {
	Store     state.ProjectMigrationStore
	Reader    Reader
	AccountID string
	DigestKey []byte
}

func (b Backend) Digest(in api.CreateProjectMigrationRequest) string {
	// The token expires independently of the durable operation. Hash the exact
	// create request with a server key; a public hash would expose low-entropy values.
	raw, _ := json.Marshal(in)
	h := hmac.New(sha256.New, b.DigestKey)
	_, _ = h.Write(raw)
	return hex.EncodeToString(h.Sum(nil))
}
func ValidateSelection(in api.CreateProjectMigrationRequest, plan api.PlanResponse) error {
	if !api.ValidProjectMigrationID(in.ID) || len(in.Only) == 0 || len(in.Only) > api.ProjectMigrationMaxServices {
		return api.ErrValidation("select between 1 and 10 HTTP or GraphQL services and supply a canonical migration UUID")
	}
	if !plan.CanApply {
		return api.ErrValidation("review the selected services and resolve the plan's deployment blockers")
	}
	seen := map[string]bool{}
	for _, name := range in.Only {
		if seen[name] {
			return api.ErrValidation("select each service once")
		}
		seen[name] = true
		index := slices.IndexFunc(plan.Workloads, func(w api.PlanWorkload) bool { return w.Name == name })
		if index < 0 {
			return api.ErrValidation("every service must belong to the reviewed source")
		}
		w := plan.Workloads[index]
		if (w.Class != "http" && w.Class != "graphql") || w.Image != "" {
			return api.ErrValidation("this workflow supports source-backed HTTP and GraphQL services; review container, worker and scheduled workloads separately")
		}
	}
	if len(plan.Workloads) != len(in.Only) {
		return api.ErrValidation("the reviewed plan must contain exactly the selected services")
	}
	return nil
}
func (b Backend) Existing(ctx context.Context, in api.CreateProjectMigrationRequest) (api.ProjectMigration, bool, error) {
	if !api.ValidProjectMigrationID(in.ID) {
		return api.ProjectMigration{}, false, api.ErrValidation("id must be a canonical UUID")
	}
	r, err := b.Store.GetProjectMigration(ctx, b.AccountID, in.ID)
	if errors.Is(err, state.ErrNotFound) {
		return api.ProjectMigration{}, false, nil
	}
	if err != nil {
		return api.ProjectMigration{}, false, err
	}
	if !hmac.Equal([]byte(r.RequestDigest), []byte(b.Digest(in))) {
		return api.ProjectMigration{}, true, state.ErrConflict
	}
	out, err := b.observe(ctx, r)
	return out, true, err
}
func (b Backend) Create(ctx context.Context, in api.CreateProjectMigrationRequest, plan api.PlanResponse, project, hash string) (state.ProjectMigrationRecord, error) {
	if err := ValidateSelection(in, plan); err != nil {
		return state.ProjectMigrationRecord{}, err
	}
	if err := validateKeys(in.InitialSecrets, in.Only); err != nil {
		return state.ProjectMigrationRecord{}, err
	}
	if err := validateConnections(in, plan); err != nil {
		return state.ProjectMigrationRecord{}, err
	}
	if err := validateBuildDependencies(in, plan); err != nil {
		return state.ProjectMigrationRecord{}, err
	}
	public, err := publicURLs(in, plan)
	if err != nil {
		return state.ProjectMigrationRecord{}, err
	}
	now := time.Now().UTC()
	r := state.ProjectMigrationRecord{AccountID: b.AccountID, RequestDigest: b.Digest(in), Report: api.ProjectMigration{ID: in.ID, Project: project, SourceSHA256: hash, Status: "prepared", CreatedAt: now, UpdatedAt: now, Services: []api.ProjectMigrationService{}}}
	for _, name := range in.Only {
		w := plan.Workloads[slices.IndexFunc(plan.Workloads, func(w api.PlanWorkload) bool { return w.Name == name })]
		slug := w.AppSlug
		if slug == "" {
			slug = name
		}
		keys := configuredKeys(in.InitialSecrets[name], in.ServiceConnections[name], public[name])
		r.Report.Services = append(r.Report.Services, api.ProjectMigrationService{Name: name, App: slug, Status: "pending", Attempt: 1, DependsOn: append([]string{}, w.DependsOn...), RequiredEnvKeys: append([]string{}, w.RequiredEnvKeys...), ConfiguredKeys: keys, BuildDependencies: slices.Clone(in.BuildDependencies[name]), ServiceConnections: maps.Clone(in.ServiceConnections[name]), PublicServiceConnections: maps.Clone(in.PublicServiceConnections[name]), PublicBuildURLs: maps.Clone(public[name])})
	}
	return b.Store.CreateProjectMigration(ctx, r)
}
func (b Backend) Get(ctx context.Context, id string) (api.ProjectMigration, error) {
	if !api.ValidProjectMigrationID(id) {
		return api.ProjectMigration{}, api.ErrValidation("id must be a canonical UUID")
	}
	r, err := b.Store.GetProjectMigration(ctx, b.AccountID, id)
	if err != nil {
		return api.ProjectMigration{}, err
	}
	return b.observe(ctx, r)
}
func (b Backend) List(ctx context.Context, project string) (api.ProjectMigrationList, error) {
	rows, err := b.Store.ListProjectMigrations(ctx, b.AccountID, project)
	out := api.ProjectMigrationList{Migrations: []api.ProjectMigration{}}
	for _, r := range rows {
		item, e := b.observe(ctx, r)
		if e != nil {
			return out, e
		}
		out.Migrations = append(out.Migrations, item)
	}
	return out, err
}

// observe follows exact, pre-reserved deployment IDs. A newer deployment is
// shown as changed, never silently adopted as migration evidence.
func (b Backend) observe(ctx context.Context, r state.ProjectMigrationRecord) (api.ProjectMigration, error) {
	out := r.Report
	out.Version = r.Version
	out.Services = append([]api.ProjectMigrationService{}, r.Report.Services...)
	applying := r.LeaseUntil.After(time.Now())
	if applying {
		until := r.LeaseUntil
		out.RetryAfter = &until
		out.Status = "applying"
	} else {
		out.Status = "tracking"
	}
	for i := range out.Services {
		s := &out.Services[i]
		s.VerificationID, s.VerificationStatus, s.CutoverID, s.CutoverStatus = "", "", "", ""
		s.BuildWait = nil
		if s.AppID == "" {
			s.Status = "pending"
			if !applying {
				out.Status = "attention"
			}
			continue
		}
		app, err := b.Reader.AppByID(ctx, s.AppID)
		if err != nil && !errors.Is(err, state.ErrNotFound) {
			return out, err
		}
		if err != nil || app.AccountID != b.AccountID || app.ProjectID != out.ProjectID || app.WorkloadName != s.Name || app.Slug != s.App || app.Status == state.AppDeleted {
			s.Status, s.Reason = "changed", "service_unavailable"
			out.Status = "attention"
			continue
		}
		if s.DeploymentID == "" || s.BuildID == "" {
			s.Status = "pending"
			if !applying {
				out.Status = "attention"
			}
			continue
		}
		d, err := b.Reader.DeploymentByID(ctx, s.DeploymentID)
		if errors.Is(err, state.ErrNotFound) {
			changed, checkErr := b.baselineChanged(ctx, r, *s)
			if checkErr != nil {
				return out, checkErr
			}
			if changed {
				s.Status, s.Reason = "changed", "deployment_replaced"
				out.Status = "attention"
				continue
			}
			s.Status = "pending"
			if !applying {
				out.Status = "attention"
			}
			continue
		}
		if err != nil {
			return out, err
		}
		if d.AppID != s.AppID || (d.Scope != "" && d.Scope != api.DefaultEnvScope) {
			return out, state.ErrConflict
		}
		latest, err := b.Reader.LatestDeploymentForScope(ctx, s.AppID, api.DefaultEnvScope)
		if err != nil {
			return out, err
		}
		if latest.ID != d.ID {
			s.Status, s.Reason = "changed", "deployment_replaced"
			out.Status = "attention"
			continue
		}
		var stages state.StageState
		if err := json.Unmarshal(d.StageState, &stages); len(d.StageState) > 0 && err != nil {
			return out, err
		}
		if gate := stages.BuildDependencyGate; gate != nil {
			s.BuildWait = &api.ProjectMigrationBuildWait{Status: gate.Status, Code: gate.Code, Detail: gate.Blocker, DeadlineAt: gate.DeadlineAt}
		}
		if d.Status.IsTerminal() {
			s.Status, s.Reason = string(d.Status), ""
			if s.BuildWait != nil && s.BuildWait.Status == "failed" {
				s.Reason = s.BuildWait.Code
			}
			out.Status = "attention"
			continue
		}
		build, err := b.Reader.BuildByID(ctx, s.BuildID)
		if errors.Is(err, state.ErrNotFound) {
			s.Status = "pending"
			if !applying {
				out.Status = "attention"
			}
			continue
		}
		if err != nil {
			return out, err
		}
		if build.DeploymentID != d.ID {
			return out, state.ErrConflict
		}
		s.Status, s.Reason = string(d.Status), ""
		if build.Status == state.BuildQueued && s.BuildWait != nil && s.BuildWait.Status == "waiting" {
			s.Status = "waiting"
		}
		if d.Status.IsTerminal() {
			out.Status = "attention"
		}
		if err := b.evidence(ctx, s); err != nil {
			return out, err
		}
	}
	if applying {
		out.Status = "applying"
	}
	if !applying && out.Status == "attention" && out.Reason == "" {
		out.Reason = "Review unfinished services before resuming."
	}
	return out, nil
}
func (b Backend) evidence(ctx context.Context, s *api.ProjectMigrationService) error {
	// Summaries are pointers to existing owners, not fresh verification claims.
	if store, ok := b.Store.(state.MigrationVerificationStore); ok {
		rows, err := store.ListMigrationVerifications(ctx, b.AccountID, s.AppID)
		if err != nil {
			return err
		}
		for _, r := range rows {
			if r.Report.DeploymentID == s.DeploymentID {
				s.VerificationID, s.VerificationStatus = r.Report.ID, r.Report.Status
				break
			}
		}
	}
	if store, ok := b.Store.(state.MigrationCutoverStore); ok {
		rows, err := store.ListMigrationCutovers(ctx, b.AccountID, s.AppID)
		if err != nil {
			return err
		}
		for _, r := range rows {
			if r.Report.DeploymentID == s.DeploymentID {
				s.CutoverID, s.CutoverStatus = r.Report.ID, r.Report.Status
				break
			}
		}
	}
	return nil
}

type runKey struct{}
type Run struct {
	backend  Backend
	record   state.ProjectMigrationRecord
	targets  map[string]bool
	deadline time.Time
}

func FromContext(ctx context.Context) *Run { r, _ := ctx.Value(runKey{}).(*Run); return r }
func (r *Run) Context(ctx context.Context) context.Context {
	return context.WithValue(ctx, runKey{}, r)
}
func (r *Run) Deadline() time.Time { return r.deadline }
func (r *Run) Names() []string {
	out := []string{}
	for _, s := range r.record.Report.Services {
		if r.targets[s.Name] {
			out = append(out, s.Name)
		}
	}
	return out
}

// ValidateAppSlugs pins unfinished allocations even if the first apply stopped
// before creating apps. Tokens from a fresh scan cannot change saved intent.
func (r *Run) ValidateAppSlugs(slugs map[string]string) error {
	return validateSavedAppSlugs(r.record.Report.Services, slugs, r.Names())
}

// CheckAppSlugs rejects changed allocation intent before claiming a lease or
// advancing an attempt. BeginReviewed subsequently fences the record version.
func (b Backend) CheckAppSlugs(ctx context.Context, id string, slugs map[string]string, only []string) error {
	if !api.ValidProjectMigrationID(id) {
		return api.ErrValidation("id must be a canonical UUID")
	}
	r, err := b.Store.GetProjectMigration(ctx, b.AccountID, id)
	if err != nil {
		return err
	}
	return validateSavedAppSlugs(r.Report.Services, slugs, only)
}

func validateSavedAppSlugs(services []api.ProjectMigrationService, slugs map[string]string, only []string) error {
	for _, s := range services {
		if !slices.Contains(only, s.Name) {
			continue
		}
		want, got := s.App, slugs[s.Name]
		if want == "" {
			want = s.Name
		}
		if got == "" {
			got = s.Name
		}
		if want != got {
			return api.ErrValidation("resume must use the saved Gregale app name for " + s.Name + ": " + want)
		}
	}
	return nil
}
func (r *Run) OperationID(appID string) string {
	for _, s := range r.record.Report.Services {
		if s.AppID == appID && r.targets[s.Name] {
			return operationID(r.record.Report.ID, s.Name, s.Attempt)
		}
	}
	return ""
}

// Begin claims a bounded lease before any intent writes. A failed CAS means no
// apply may start. Retries preserve every accepted nonterminal deployment.
func (b Backend) Begin(ctx context.Context, id, hash, project string, values api.ProjectInitialSecrets) (*Run, error) {
	return b.begin(ctx, id, hash, project, values, 0, nil)
}

// BeginReviewed fences a resume to the record and service set the customer saw.
func (b Backend) BeginReviewed(ctx context.Context, id, hash, project string, values api.ProjectInitialSecrets, version int64, only []string) (*Run, error) {
	if version <= 0 || len(only) == 0 || len(only) > api.ProjectMigrationMaxServices {
		return nil, api.ErrValidation("review the saved migration version and name the unfinished services to resume")
	}
	return b.begin(ctx, id, hash, project, values, version, only)
}
func (b Backend) begin(ctx context.Context, id, hash, project string, values api.ProjectInitialSecrets, version int64, only []string) (*Run, error) {
	if !api.ValidProjectMigrationID(id) {
		return nil, api.ErrValidation("id must be a canonical UUID")
	}
	r, err := b.Store.GetProjectMigration(ctx, b.AccountID, id)
	if err != nil {
		return nil, err
	}
	if version > 0 && r.Version != version {
		return nil, state.ErrConflict
	}
	seen := map[string]bool{}
	for _, name := range only {
		if seen[name] || !slices.ContainsFunc(r.Report.Services, func(s api.ProjectMigrationService) bool { return s.Name == name }) {
			return nil, api.ErrValidation("resume must name unique services from this saved migration")
		}
		seen[name] = true
	}
	if r.Report.SourceSHA256 != hash || r.Report.Project != project {
		return nil, api.ErrValidation("rescan the original source revision and project before resuming this migration")
	}
	if r.LeaseUntil.After(time.Now()) {
		return nil, state.ErrConflict
	}
	current, err := b.observe(ctx, r)
	if err != nil {
		return nil, err
	}
	run := &Run{backend: b, record: r, targets: map[string]bool{}, deadline: time.Now().Add(api.ProjectApplyTimeout)}
	for i, s := range current.Services {
		if s.Status == "changed" {
			return nil, api.ErrValidation("a migration service or deployment changed; review it in the app before creating a new migration")
		}
		if len(only) > 0 && !seen[s.Name] {
			continue
		}
		if s.Status == "pending" || s.Status == "failed" || s.Status == "cancelled" || s.Status == "superseded" {
			if s.Status != "pending" {
				if r.Report.Services[i].Attempt >= api.ProjectMigrationMaxAttempts {
					return nil, state.ErrConflict
				}
				run.record.Report.Services[i].Attempt++
				if run.record.BeforeDeploymentIDs == nil {
					run.record.BeforeDeploymentIDs = map[string]string{}
				}
				nextOperation := operationID(run.record.Report.ID, s.Name, run.record.Report.Services[i].Attempt)
				run.record.BeforeDeploymentIDs[nextOperation] = s.DeploymentID
				run.record.Report.Services[i].DeploymentID = ""
				run.record.Report.Services[i].BuildID = ""
			}
			run.targets[s.Name] = true
			run.record.Report.Services[i].Status = "pending"
			run.record.Report.Services[i].Reason = ""
		}
	}
	if len(run.targets) == 0 {
		return nil, api.ErrValidation("all services already have accepted deployments; follow their progress without deploying again")
	}
	if err := validateKeys(values, run.Names()); err != nil {
		return nil, err
	}
	if err := run.validateConnectionValues(values); err != nil {
		return nil, err
	}
	for name, entries := range values {
		if !run.targets[name] {
			return nil, api.ErrValidation("configuration may only target unfinished migration services")
		}
		for i := range run.record.Report.Services {
			s := &run.record.Report.Services[i]
			if s.Name != name {
				continue
			}
			for key := range entries {
				if !slices.Contains(s.ConfiguredKeys, key) {
					s.ConfiguredKeys = append(s.ConfiguredKeys, key)
				}
			}
			sort.Strings(s.ConfiguredKeys)
		}
	}
	run.record.Report.Status = "applying"
	run.record.Report.Reason = ""
	run.record.Report.UpdatedAt = time.Now().UTC()
	run.record.LeaseUntil = run.deadline.Add(api.ProjectMigrationLeaseGrace)
	run.record, err = b.Store.SaveProjectMigration(ctx, run.record, r.Version)
	return run, err
}

// Prepare runs after all configuration has been sealed, before the first build.
// Even an unchanged selected workload needs its first migration deployment.
func (r *Run) Prepare(ctx context.Context, project string, apps []state.App) ([]state.App, error) {
	if r.record.Report.ProjectID != "" && r.record.Report.ProjectID != project {
		return nil, state.ErrConflict
	}
	r.record.Report.ProjectID = project
	if err := r.configurePublicURLs(ctx, project, apps); err != nil {
		return nil, err
	}
	out := []state.App{}
	for i := range r.record.Report.Services {
		s := &r.record.Report.Services[i]
		if !r.targets[s.Name] {
			continue
		}
		index := slices.IndexFunc(apps, func(a state.App) bool {
			return a.WorkloadName == s.Name && a.AccountID == r.backend.AccountID && a.ProjectID == project && a.Status != state.AppDeleted
		})
		if index < 0 {
			return nil, state.ErrNotFound
		}
		app := apps[index]
		if s.AppID != "" && s.AppID != app.ID || s.App != "" && s.App != app.Slug {
			return nil, state.ErrConflict
		}
		if err := r.checkConfiguration(ctx, app, *s); err != nil {
			return nil, err
		}
		s.AppID, s.App = app.ID, app.Slug
		operation := r.OperationID(app.ID)
		latest, err := r.backend.Reader.LatestDeploymentForScope(ctx, app.ID, api.DefaultEnvScope)
		if err != nil && !errors.Is(err, state.ErrNotFound) {
			return nil, err
		}
		if r.record.BeforeDeploymentIDs == nil {
			r.record.BeforeDeploymentIDs = map[string]string{}
		}
		if expected, ok := r.record.BeforeDeploymentIDs[operation]; ok {
			if latest.ID != expected && latest.ID != s.DeploymentID {
				return nil, state.ErrConflict
			}
		} else {
			r.record.BeforeDeploymentIDs[operation] = latest.ID
		}

		s.DeploymentID, s.BuildID = apidsource.OperationIDs(r.OperationID(app.ID), app.ID)
		out = append(out, app)
	}
	var err error
	r.record, err = r.backend.Store.SaveProjectMigration(ctx, r.record, r.record.Version)
	return out, err
}
func (r *Run) Finish(ctx context.Context, builds []api.AppliedBuild, failed bool) error {
	// Values and low-level error strings never enter the receipt.
	for i := range r.record.Report.Services {
		s := &r.record.Report.Services[i]
		if !r.targets[s.Name] {
			continue
		}
		for _, build := range builds {
			if build.AppID == s.AppID && build.Error != "" {
				s.Reason = "deployment_admission_incomplete"
			}
		}
	}
	r.record.LeaseUntil = time.Time{}
	r.record.Report.Status = "tracking"
	if failed {
		r.record.Report.Status = "attention"
		r.record.Report.Reason = "Project apply stopped. Review unfinished service configuration and retry."
	}
	r.record.Report.UpdatedAt = time.Now().UTC()
	persist, cancel := context.WithTimeout(context.WithoutCancel(ctx), api.ProjectMigrationPersistTimeout)
	defer cancel()
	var err error
	r.record, err = r.backend.Store.SaveProjectMigration(persist, r.record, r.record.Version)
	return err
}

func validateKeys(values api.ProjectInitialSecrets, names []string) error {
	for name, entries := range values {
		if !slices.Contains(names, name) {
			return api.ErrValidation("configuration must target an exactly named unfinished migration service")
		}
		for key := range entries {
			if problem := api.ValidateSecretKey(key); problem != nil {
				return problem
			}
		}
	}
	return nil
}

func operationID(id, name string, attempt int) string {
	return fmt.Sprintf("%s/%s/%d", id, name, attempt)
}
func (b Backend) baselineChanged(ctx context.Context, r state.ProjectMigrationRecord, s api.ProjectMigrationService) (bool, error) {
	expected, ok := r.BeforeDeploymentIDs[operationID(r.Report.ID, s.Name, s.Attempt)]
	if !ok {
		return false, nil
	}
	latest, err := b.Reader.LatestDeploymentForScope(ctx, s.AppID, api.DefaultEnvScope)
	if errors.Is(err, state.ErrNotFound) {
		return expected != "", nil
	}
	if err != nil {
		return false, err
	}
	return latest.ID != expected && latest.ID != s.DeploymentID, nil
}
func (r *Run) checkConfiguration(ctx context.Context, app state.App, s api.ProjectMigrationService) error {
	if len(s.ConfiguredKeys) == 0 {
		return nil
	}
	reader, ok := r.backend.Reader.(interface {
		ListAllAppSecrets(context.Context, string, string) ([]state.AppSecret, error)
	})
	if !ok {
		return api.ErrCapacity("migration configuration metadata is unavailable")
	}
	rows, err := reader.ListAllAppSecrets(ctx, r.backend.AccountID, app.ID)
	if err != nil {
		return err
	}
	for _, key := range s.ConfiguredKeys {
		if _, public := s.PublicBuildURLs[key]; public {
			continue
		}
		if !slices.ContainsFunc(rows, func(row state.AppSecret) bool { return row.Key == key && row.Scope == api.DefaultEnvScope }) {
			return api.ErrValidation("configuration is incomplete for " + s.Name + "; resubmit any original configuration keys that were not saved")
		}
	}
	return nil
}
