// adr:1661 — a cutover plan records customer intent, never edits provider DNS.
package migrationcutover

import (
	"context"
	"errors"
	"reflect"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type Backend struct {
	Store                 state.MigrationCutoverStore
	AccountID, AppID, App string
	Verification          func(context.Context, string) (api.MigrationVerification, error)
	Domain                func(context.Context, string) (state.CustomDomain, error)
	Instructions          func(state.CustomDomain) []api.DNSRecordInstruction
	Probe                 func(context.Context, api.MigrationCutover) ([]api.MigrationVerificationCheck, error)
	Traffic               func(context.Context, api.MigrationCutover) api.MigrationCutoverTraffic
}

func limitedTraffic(reason string) api.MigrationCutoverTraffic {
	return api.MigrationCutoverTraffic{Status: "unknown", Coverage: "deployment_route_observed_only", Reason: reason}
}
func validID(id string) bool {
	parsed, err := uuid.Parse(id)
	return err == nil && parsed != uuid.Nil && parsed.String() == id
}

func (b Backend) Create(ctx context.Context, in api.CreateMigrationCutoverRequest) (api.MigrationCutover, error) {
	if err := in.Validate(); err != nil {
		return api.MigrationCutover{}, err
	}
	if old, err := b.Store.GetMigrationCutover(ctx, b.AccountID, b.AppID, in.ID); err == nil {
		if !reflect.DeepEqual(old.Report.Request, in) {
			return old.Report, state.ErrConflict
		}
		return b.observe(ctx, old)
	} else if !errors.Is(err, state.ErrNotFound) {
		return api.MigrationCutover{}, err
	}
	v, err := b.Verification(ctx, in.VerificationID)
	if err != nil {
		return api.MigrationCutover{}, err
	}
	if v.Status != "passed" || v.Scope != "default" {
		return api.MigrationCutover{}, api.ErrValidation("prepare a production switch after verification passes for the current default deployment")
	}
	r := state.MigrationCutoverRecord{AccountID: b.AccountID, AppID: b.AppID, Report: api.MigrationCutover{
		ID: in.ID, App: b.App, DeploymentID: v.DeploymentID, ConfigurationRevision: v.ConfigurationRevision, SmokePath: v.Request.SmokePath,
		Status: "prepared", Request: in, DNSRecords: []api.DNSRecordInstruction{}, Checks: []api.MigrationVerificationCheck{}, Traffic: limitedTraffic("checks_not_started"), CreatedAt: time.Now().UTC(),
	}}
	if err := b.refresh(ctx, &r); err != nil {
		return r.Report, err
	}
	if r.Report.Status == "blocked" {
		return r.Report, state.ErrConflict
	}
	if r.Report.DomainConnected {
		d, err := b.Domain(ctx, in.Hostname)
		if err != nil {
			return r.Report, err
		}
		r.BindingRevision = state.MigrationDomainRevision(d)
	}
	saved, err := b.Store.CreateMigrationCutover(ctx, r)
	return saved.Report, err
}

func (b Backend) Get(ctx context.Context, id string) (api.MigrationCutover, error) {
	if !validID(id) {
		return api.MigrationCutover{}, state.ErrNotFound
	}
	r, err := b.Store.GetMigrationCutover(ctx, b.AccountID, b.AppID, id)
	if err != nil {
		return r.Report, err
	}
	return b.observe(ctx, r)
}
func (b Backend) List(ctx context.Context) (api.MigrationCutoverList, error) {
	rows, err := b.Store.ListMigrationCutovers(ctx, b.AccountID, b.AppID)
	out := api.MigrationCutoverList{Plans: []api.MigrationCutover{}}
	if err != nil {
		return out, err
	}
	for _, r := range rows {
		report, err := b.observe(ctx, r)
		if err != nil {
			return out, err
		}
		out.Plans = append(out.Plans, report)
	}
	return out, nil
}
func (b Backend) observe(ctx context.Context, r state.MigrationCutoverRecord) (api.MigrationCutover, error) {
	if r.Report.Status == "cancelled" || r.Report.Status == "restored" {
		return r.Report, nil
	}
	err := b.refresh(ctx, &r)
	return r.Report, err
}

func (b Backend) refresh(ctx context.Context, r *state.MigrationCutoverRecord) error {
	v, err := b.Verification(ctx, r.Report.Request.VerificationID)
	if errors.Is(err, state.ErrNotFound) {
		r.Report.Status, r.Report.Reason = "blocked", "verification_unavailable"
		return nil
	}
	if err != nil {
		return err
	}
	if v.Status != "passed" || v.Scope != "default" || v.DeploymentID != r.Report.DeploymentID || v.ConfigurationRevision != r.Report.ConfigurationRevision {
		r.Report.Status, r.Report.Reason = "blocked", "deployment_or_configuration_changed"
		return nil
	}
	d, err := b.Domain(ctx, r.Report.Request.Hostname)
	r.Report.DomainConnected = false
	r.Report.DNSRecords = []api.DNSRecordInstruction{}
	if errors.Is(err, state.ErrNotFound) {
		if r.BindingRevision != "" {
			r.Report.Status, r.Report.Reason = "blocked", "domain_binding_changed"
		}
		return nil
	}
	if err != nil {
		return err
	}
	if d.AppID != b.AppID || d.EnvironmentID != "" || r.BindingRevision != "" && state.MigrationDomainRevision(d) != r.BindingRevision {
		r.Report.Status, r.Report.Reason = "blocked", "domain_binding_changed"
		return nil
	}
	r.Report.DomainConnected = true
	r.Report.DNSRecords = b.Instructions(d)
	return nil
}

func (b Backend) Act(ctx context.Context, id string, in api.MigrationCutoverActionRequest) (api.MigrationCutover, error) {
	if !validID(id) {
		return api.MigrationCutover{}, state.ErrNotFound
	}
	if in.Action != "start_checks" && in.Action != "complete" && in.Action != "cancel" && in.Action != "confirm_restored" {
		return api.MigrationCutover{}, api.ErrValidation("unknown cutover action")
	}
	if in.Action == "complete" {
		return b.step(ctx, id, &in)
	}
	r, err := b.Store.GetMigrationCutover(ctx, b.AccountID, b.AppID, id)
	if err != nil {
		return r.Report, err
	}
	switch in.Action {
	case "cancel", "confirm_restored":
		status := "cancelled"
		if in.Action == "confirm_restored" {
			status = "restored"
		}
		if r.Report.Status == status {
			return r.Report, nil
		}
		r.Report.Status, r.Report.Reason = status, ""
		now := time.Now().UTC()
		r.Report.CompletedAt = &now
		r.LeaseUntil, r.NextAttemptAt = time.Time{}, time.Time{}
	case "start_checks":
		if r.Report.Status == "cancelled" || r.Report.Status == "restored" || r.Report.Status == "completed" || r.Report.Status == "blocked" {
			return r.Report, state.ErrConflict
		}
		if err := b.refresh(ctx, &r); err != nil {
			return r.Report, err
		}
		if r.Report.Status == "blocked" || !r.Report.DomainConnected {
			return r.Report, api.ErrValidation("connect this hostname to this app's default deployment before starting checks")
		}
		if r.Report.Status == "checking" || r.Report.Status == "ready" {
			return r.Report, nil
		}
		d, err := b.Domain(ctx, r.Report.Request.Hostname)
		if err != nil {
			return r.Report, err
		}
		if d.AppID != b.AppID || d.EnvironmentID != "" {
			return r.Report, state.ErrConflict
		}
		if r.BindingRevision != "" && r.BindingRevision != state.MigrationDomainRevision(d) {
			return r.Report, state.ErrConflict
		}
		r.BindingRevision = state.MigrationDomainRevision(d)
		now := time.Now().UTC()
		deadline := now.Add(api.MigrationCutoverCheckDuration)
		r.Report.Status, r.Report.Reason = "checking", ""
		r.Report.StartedAt, r.Report.CheckDeadline = &now, &deadline
		r.Report.HostnameVerifiedAt, r.Report.CheckedAt = nil, nil
		r.Report.Checks = []api.MigrationVerificationCheck{}
		r.Report.Traffic = limitedTraffic("waiting_for_hostname_proof")
		r.NextAttemptAt = now
	}
	saved, err := b.Store.SaveMigrationCutover(ctx, r, r.Version)
	return saved.Report, err
}

func (b Backend) Advance(ctx context.Context, id string) (api.MigrationCutover, error) {
	return b.step(ctx, id, nil)
}

func (b Backend) step(ctx context.Context, id string, completion *api.MigrationCutoverActionRequest) (api.MigrationCutover, error) {
	if !validID(id) {
		return api.MigrationCutover{}, state.ErrNotFound
	}
	r, err := b.Store.GetMigrationCutover(ctx, b.AccountID, b.AppID, id)
	if err != nil {
		return r.Report, err
	}
	if completion != nil && r.Report.Status == "completed" {
		return b.observe(ctx, r)
	}
	if r.Report.Status != "checking" && r.Report.Status != "ready" {
		if completion != nil {
			return r.Report, state.ErrConflict
		}
		return b.observe(ctx, r)
	}
	if r.LeaseUntil.After(time.Now()) {
		if completion != nil {
			return r.Report, state.ErrConflict
		}
		return b.observe(ctx, r)
	}
	r.LeaseUntil = time.Now().Add(api.MigrationCutoverLease)
	r, err = b.Store.SaveMigrationCutover(ctx, r, r.Version)
	if errors.Is(err, state.ErrConflict) {
		return b.Get(ctx, id)
	}
	if err != nil {
		return r.Report, err
	}
	workCtx, cancel := context.WithDeadline(ctx, r.LeaseUntil.Add(-api.MigrationVerificationPersistTimeout))
	defer cancel()
	if err := b.evaluate(workCtx, &r); err != nil {
		return r.Report, err
	}
	var completionErr error
	if completion != nil {
		switch {
		case r.Report.Status != "ready" || r.Report.Traffic.Status == "violated":
			completionErr = state.ErrConflict
		case r.Report.Traffic.Status != "healthy" && !completion.AcknowledgeLimitedTraffic:
			completionErr = api.ErrValidation("traffic evidence is limited; explicitly acknowledge that limitation to complete this switch")
		default:
			r.Report.Status = "completed"
			now := time.Now().UTC()
			r.Report.CompletedAt = &now
			r.Report.LimitedTrafficAcknowledged = r.Report.Traffic.Status != "healthy"
		}
	}
	r.LeaseUntil = time.Time{}
	r.NextAttemptAt = time.Now().Add(api.MigrationCutoverCheckInterval)
	persistCtx, persistCancel := context.WithTimeout(context.WithoutCancel(ctx), api.MigrationVerificationPersistTimeout)
	defer persistCancel()
	saved, err := b.Store.SaveMigrationCutover(persistCtx, r, r.Version)
	if errors.Is(err, state.ErrConflict) {
		return b.Get(ctx, id)
	}
	if err != nil {
		return r.Report, err
	}
	return saved.Report, completionErr
}

func (b Backend) evaluate(ctx context.Context, r *state.MigrationCutoverRecord) error {
	if err := b.refresh(ctx, r); err != nil {
		return err
	}
	if r.Report.Status == "blocked" {
		return nil
	}
	if r.Report.CheckDeadline == nil || !r.Report.CheckDeadline.After(time.Now()) {
		r.Report.Status, r.Report.Reason = "attention", "check_deadline_reached"
		return nil
	}
	checks, err := b.Probe(ctx, r.Report)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	r.Report.Checks, r.Report.CheckedAt = checks, &now
	passed := len(checks) > 0
	for _, check := range checks {
		passed = passed && check.Status == "passed"
	}
	r.Report.Status, r.Report.Reason = "checking", "waiting_for_dns_tls_or_route"
	if passed {
		r.Report.Status, r.Report.Reason = "ready", ""
		if r.Report.HostnameVerifiedAt == nil {
			r.Report.HostnameVerifiedAt = &now
		}
		r.Report.Traffic = b.Traffic(ctx, r.Report)
	} else {
		r.Report.HostnameVerifiedAt = nil
		r.Report.Traffic = limitedTraffic("waiting_for_hostname_proof")
	}
	// Evidence is accepted only if the same deployment and domain binding still
	// own the hostname after all network I/O. CAS then fences concurrent cancel.
	if !r.Report.CheckDeadline.After(time.Now()) {
		r.Report.Status, r.Report.Reason = "attention", "check_deadline_reached"
	}
	return b.refresh(ctx, r)
}
