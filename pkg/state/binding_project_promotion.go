// adr: 623 — leases fence exact promotion candidates and completion receipts.
package state

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var ErrBindingProjectPromotionLease = errors.New("binding project promotion lease changed or expired")

type BindingProjectPromotionClaim struct {
	TargetDeploymentID                                      string
	PromotionID, AccountID, ProjectSlug, Environment, Token string
	Until                                                   time.Time
}

type BindingProjectPromotionStore interface {
	ClaimBindingProjectPromotion(context.Context) (BindingProjectPromotionClaim, error)
	ReserveBindingProjectPromotionTarget(context.Context, BindingProjectPromotionClaim, string) (string, error)
	CheckpointBindingProjectPromotionTarget(context.Context, BindingProjectPromotionClaim, string) error
	UpdateBindingProjectPromotionCheck(context.Context, BindingProjectPromotionClaim, api.ProjectReleaseCheckResponse, string, bool) error
	PublishBindingCheckedEnvironmentPromotion(context.Context, BindingProjectPromotionClaim, int, []ProjectReleaseMember, api.ProjectReleaseCheckResponse) (ProjectReleaseSet, error)
}

type bindingProjectPromotionClaimKey struct{}
type bindingProjectPromotionReportKey struct{}

func WithBindingProjectPromotionClaim(ctx context.Context, claim BindingProjectPromotionClaim) context.Context {
	return context.WithValue(ctx, bindingProjectPromotionClaimKey{}, claim)
}
func bindingProjectPromotionClaim(ctx context.Context) BindingProjectPromotionClaim {
	claim, _ := ctx.Value(bindingProjectPromotionClaimKey{}).(BindingProjectPromotionClaim)
	return claim
}
func bindingProjectPromotionReport(ctx context.Context) (api.ProjectReleaseCheckResponse, bool) {
	report, ok := ctx.Value(bindingProjectPromotionReportKey{}).(api.ProjectReleaseCheckResponse)
	return report, ok
}
func (m *MemStore) validBindingProjectPromotionClaimLocked(claim BindingProjectPromotionClaim) error {
	p, ok := m.projectEnvironmentPromotions[claim.PromotionID]
	if !ok || p.AccountID != claim.AccountID || !p.BindingsRequired || p.Status != "running" || p.TargetReleaseSetID != "" ||
		p.BindingWorkerToken != claim.Token || claim.Token == "" || p.BindingWorkerUntil == nil || (!p.BindingWorkerUntil.Equal(claim.Until) || !p.BindingWorkerUntil.After(time.Now())) {
		return ErrBindingProjectPromotionLease
	}
	return nil
}
func lockBindingProjectPromotionClaim(ctx context.Context, tx pgx.Tx, claim BindingProjectPromotionClaim) error {
	if claim.Token == "" {
		return ErrBindingProjectPromotionLease
	}
	row, err := sqlc.New().LockBindingProjectPromotion(ctx, tx, sqlc.LockBindingProjectPromotionParams{PromotionID: mustPgUUID(claim.PromotionID), AccountID: mustPgUUID(claim.AccountID), Token: mustPgUUID(claim.Token)})
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrBindingProjectPromotionLease
	}
	if err != nil {
		return mapErr(err)
	}
	if !row.BindingsRequired || row.Status != "running" || row.TargetReleaseID != "" || !row.BindingWorkerUntil.Valid || (!row.BindingWorkerUntil.Time.Equal(claim.Until) || !row.BindingWorkerUntil.Time.After(time.Now())) {
		return ErrBindingProjectPromotionLease
	}
	return nil
}
func (m *MemStore) ClaimBindingProjectPromotion(_ context.Context) (BindingProjectPromotionClaim, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().UTC()
	var ids []string
	for id, p := range m.projectEnvironmentPromotions {
		if p.BindingsRequired && p.Status == "running" && p.TargetReleaseSetID == "" && (p.BindingCheckNextAt == nil || !p.BindingCheckNextAt.After(now)) && (p.BindingWorkerUntil == nil || !p.BindingWorkerUntil.After(now)) {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool {
		a, b := m.projectEnvironmentPromotions[ids[i]], m.projectEnvironmentPromotions[ids[j]]
		if a.BindingCheckNextAt == nil && b.BindingCheckNextAt != nil {
			return true
		}
		if a.BindingCheckNextAt != nil && b.BindingCheckNextAt == nil {
			return false
		}
		if a.BindingCheckNextAt != nil && !a.BindingCheckNextAt.Equal(*b.BindingCheckNextAt) {
			return a.BindingCheckNextAt.Before(*b.BindingCheckNextAt)
		}
		if !a.CreatedAt.Equal(b.CreatedAt) {
			return a.CreatedAt.Before(b.CreatedAt)
		}
		return ids[i] < ids[j]
	})
	if len(ids) == 0 {
		return BindingProjectPromotionClaim{}, ErrNotFound
	}
	p := m.projectEnvironmentPromotions[ids[0]]
	p.BindingWorkerToken = uuid.NewString()
	until := now.Add(time.Duration(api.ProjectBindingPromotionLeaseSeconds) * time.Second)
	p.BindingWorkerUntil = &until
	m.projectEnvironmentPromotions[p.ID] = p
	return BindingProjectPromotionClaim{PromotionID: p.ID, AccountID: p.AccountID, ProjectSlug: p.ProjectSlug, Environment: p.ToEnvironment, Token: p.BindingWorkerToken, Until: until}, nil
}
func (s *PgStore) ClaimBindingProjectPromotion(ctx context.Context) (BindingProjectPromotionClaim, error) {
	row, err := sqlc.New().ClaimBindingProjectPromotion(ctx, s.pool, sqlc.ClaimBindingProjectPromotionParams{Token: mustPgUUID(uuid.NewString()), LeaseSeconds: api.ProjectBindingPromotionLeaseSeconds})
	if err != nil {
		return BindingProjectPromotionClaim{}, mapErr(err)
	}
	return BindingProjectPromotionClaim{PromotionID: uuidString(row.ID), AccountID: uuidString(row.AccountID), ProjectSlug: row.ProjectSlug, Environment: row.ToEnvironment, Token: uuidString(row.BindingWorkerToken), Until: row.BindingWorkerUntil.Time}, nil
}
func (m *MemStore) ReserveBindingProjectPromotionTarget(_ context.Context, claim BindingProjectPromotionClaim, workloadID string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.validBindingProjectPromotionClaimLocked(claim); err != nil {
		return "", err
	}
	rows := m.projectEnvironmentPromotionWorkloads[claim.PromotionID]
	for i, w := range rows {
		if w.ID == workloadID && w.Status == "pending" {
			if w.TargetDeploymentID == "" {
				w.TargetDeploymentID = uuid.NewString()
				w.UpdatedAt = time.Now().UTC()
				rows[i] = w
				m.projectEnvironmentPromotionWorkloads[claim.PromotionID] = rows
			}
			return w.TargetDeploymentID, nil
		}
	}
	return "", ErrConflict
}
func (s *PgStore) ReserveBindingProjectPromotionTarget(ctx context.Context, claim BindingProjectPromotionClaim, workloadID string) (string, error) {
	var target string
	err := s.bindingProjectPromotionWrite(ctx, claim, func(tx pgx.Tx) error {
		row, err := sqlc.New().ReserveBindingProjectPromotionTarget(ctx, tx, sqlc.ReserveBindingProjectPromotionTargetParams{DeploymentID: uuid.NewString(), WorkloadID: mustPgUUID(workloadID), PromotionID: mustPgUUID(claim.PromotionID), AccountID: mustPgUUID(claim.AccountID), Token: mustPgUUID(claim.Token)})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrBindingProjectPromotionLease
		}
		target = row.TargetDeploymentID
		return mapErr(err)
	})
	return target, err
}

func (m *MemStore) CheckpointBindingProjectPromotionTarget(_ context.Context, claim BindingProjectPromotionClaim, workloadID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.validBindingProjectPromotionClaimLocked(claim); err != nil {
		return err
	}
	p := m.projectEnvironmentPromotions[claim.PromotionID]
	rows := m.projectEnvironmentPromotionWorkloads[p.ID]
	for i, w := range rows {
		if w.ID != workloadID || w.Status != "pending" {
			continue
		}
		d, ok := m.deployments[w.TargetDeploymentID]
		app := m.apps[d.AppID]
		if !ok || d.Status != DeployLive || d.Scope != p.ToEnvironment || d.RootfsKey == "" || d.ImageDigest == "" || app.AccountID != p.AccountID || app.ProjectID != p.ProjectID || app.Slug != w.WorkloadSlug {
			return ErrConflict
		}
		w.Status = "promoted"
		w.Error = ""
		w.UpdatedAt = time.Now().UTC()
		rows[i] = w
		m.projectEnvironmentPromotionWorkloads[p.ID] = rows
		return nil
	}
	return ErrConflict
}
func bindingProjectPromotionRows(count int64, err error) error {
	if err != nil {
		return mapErr(err)
	}
	if count != 1 {
		return ErrBindingProjectPromotionLease
	}
	return nil
}
func (s *PgStore) CheckpointBindingProjectPromotionTarget(ctx context.Context, claim BindingProjectPromotionClaim, workloadID string) error {
	return s.bindingProjectPromotionWrite(ctx, claim, func(tx pgx.Tx) error {
		count, err := sqlc.New().CheckpointBindingProjectPromotionTarget(ctx, tx, sqlc.CheckpointBindingProjectPromotionTargetParams{WorkloadID: mustPgUUID(workloadID), PromotionID: mustPgUUID(claim.PromotionID), AccountID: mustPgUUID(claim.AccountID), Token: mustPgUUID(claim.Token)})
		return bindingProjectPromotionRows(count, err)
	})
}

func (m *MemStore) UpdateBindingProjectPromotionCheck(_ context.Context, claim BindingProjectPromotionClaim, report api.ProjectReleaseCheckResponse, message string, failed bool) error {
	if report.Passed {
		return ErrInvalidArgument
	}
	raw, err := json.Marshal(report)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.validBindingProjectPromotionClaimLocked(claim); err != nil {
		return err
	}
	p := m.projectEnvironmentPromotions[claim.PromotionID]
	p.BindingsCheck = raw
	p.Error = message
	p.BindingWorkerToken = ""
	p.BindingWorkerUntil = nil
	now := time.Now().UTC()
	next := now.Add(time.Duration(api.ProjectBindingPromotionCheckIntervalSeconds) * time.Second)
	p.BindingCheckNextAt = &next
	p.UpdatedAt = now
	if failed {
		p.Status = "failed"
		p.CompletedAt = &now
	}
	m.projectEnvironmentPromotions[p.ID] = p
	return nil
}
func (s *PgStore) UpdateBindingProjectPromotionCheck(ctx context.Context, claim BindingProjectPromotionClaim, report api.ProjectReleaseCheckResponse, message string, failed bool) error {
	if report.Passed {
		return ErrInvalidArgument
	}
	raw, err := json.Marshal(report)
	if err != nil {
		return err
	}
	return s.bindingProjectPromotionWrite(ctx, claim, func(tx pgx.Tx) error {
		count, err := sqlc.New().UpdateBindingProjectPromotionCheck(ctx, tx, sqlc.UpdateBindingProjectPromotionCheckParams{Report: raw, Message: message, Failed: failed, RetrySeconds: api.ProjectBindingPromotionCheckIntervalSeconds, PromotionID: mustPgUUID(claim.PromotionID), AccountID: mustPgUUID(claim.AccountID), Token: mustPgUUID(claim.Token)})
		return bindingProjectPromotionRows(count, err)
	})
}
func (s *PgStore) bindingProjectPromotionWrite(ctx context.Context, claim BindingProjectPromotionClaim, write func(pgx.Tx) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return mapErr(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockBindingProjectPromotionClaim(ctx, tx, claim); err != nil {
		return err
	}
	if err := write(tx); err != nil {
		return err
	}
	if !claim.Until.After(time.Now()) {
		return ErrBindingProjectPromotionLease
	}
	return mapErr(tx.Commit(ctx))
}

func checkedEnvironmentPromotionContext(ctx context.Context, claim BindingProjectPromotionClaim, report api.ProjectReleaseCheckResponse) context.Context {
	ctx = WithBindingProjectPromotionClaim(ctx, claim)
	ctx = context.WithValue(ctx, checkedProjectReleaseKey{}, true)
	return context.WithValue(ctx, bindingProjectPromotionReportKey{}, report)
}
func (m *MemStore) PublishBindingCheckedEnvironmentPromotion(ctx context.Context, claim BindingProjectPromotionClaim, ttl int, members []ProjectReleaseMember, report api.ProjectReleaseCheckResponse) (ProjectReleaseSet, error) {
	return m.PublishProjectEnvironmentPromotionReleaseSet(checkedEnvironmentPromotionContext(ctx, claim, report), claim.AccountID, claim.PromotionID, ttl, members)
}
func (s *PgStore) PublishBindingCheckedEnvironmentPromotion(ctx context.Context, claim BindingProjectPromotionClaim, ttl int, members []ProjectReleaseMember, report api.ProjectReleaseCheckResponse) (ProjectReleaseSet, error) {
	return s.PublishProjectEnvironmentPromotionReleaseSet(checkedEnvironmentPromotionContext(ctx, claim, report), claim.AccountID, claim.PromotionID, ttl, members)
}
func validateBindingProjectPromotionReport(p ProjectEnvironmentPromotion, ttl int, members []ProjectReleaseMember, report api.ProjectReleaseCheckResponse, fences []BindingPromotionFence) error {
	if !p.BindingsRequired || !report.Passed || len(report.Blockers) > 0 || !sameDeploymentID(report.ProjectID, p.ProjectID) || report.Environment != p.ToEnvironment || report.TTLSeconds != ttl || report.ExpectedActiveReleaseID != p.PreviousTargetReleaseSetID || report.GraphDigest != api.ProjectReleaseGraphDigest(report) || len(report.Members) != len(members) || len(report.Checks) != len(members) || len(fences) != len(members) {
		return ErrBindingPromotionChanged
	}
	for _, member := range members {
		found, checked, fenced := false, false, false
		for _, r := range report.Members {
			found = found || sameDeploymentID(member.AppID, r.AppID) && sameDeploymentID(member.DeploymentID, r.DeploymentID)
		}
		for _, r := range report.Checks {
			checked = checked || r.Passed && r.Scope == p.ToEnvironment && sameDeploymentID(member.DeploymentID, r.DeploymentID)
		}
		for _, f := range fences {
			fenced = fenced || sameDeploymentID(member.AppID, f.AppID) && sameDeploymentID(member.DeploymentID, f.DeploymentID) && f.AccountID == p.AccountID && f.Scope == p.ToEnvironment && !f.AllowUnsupported
		}
		if !found || !checked || !fenced {
			return ErrBindingPromotionChanged
		}
	}
	return nil
}
func bindingProjectPromotionReasonID(reason string) string {
	if !strings.HasPrefix(reason, "environment promotion/") {
		return ""
	}
	id := strings.TrimPrefix(reason, "environment promotion/")
	if uuid.Validate(id) != nil {
		return ""
	}
	return id
}

func WithBindingProjectPromotionTarget(ctx context.Context, claim BindingProjectPromotionClaim, target string) context.Context {
	claim.TargetDeploymentID = target
	return WithBindingProjectPromotionClaim(ctx, claim)
}
func BindingProjectPromotionTargetID(ctx context.Context, promotionID string) string {
	claim := bindingProjectPromotionClaim(ctx)
	if claim.PromotionID != promotionID {
		return ""
	}
	return claim.TargetDeploymentID
}

func (m *MemStore) bindingProjectPromotionDeploymentLocked(ctx context.Context, d Deployment) (bool, Deployment, error) {
	id := bindingProjectPromotionReasonID(d.Reason)
	p, ok := m.projectEnvironmentPromotions[id]
	if !ok || !p.BindingsRequired {
		return false, Deployment{}, nil
	}
	claim := bindingProjectPromotionClaim(ctx)
	if claim.PromotionID != id {
		return true, Deployment{}, ErrBindingProjectPromotionLease
	}
	if err := m.validBindingProjectPromotionClaimLocked(claim); err != nil {
		return true, Deployment{}, err
	}
	app := m.apps[d.AppID]
	matched := false
	for _, w := range m.projectEnvironmentPromotionWorkloads[id] {
		matched = matched || w.WorkloadSlug == app.Slug && w.Status == "pending" && w.TargetDeploymentID == d.ID && d.ID != ""
	}
	if !matched || app.AccountID != p.AccountID || app.ProjectID != p.ProjectID || d.Scope != p.ToEnvironment || d.TrafficPercent != 0 || !d.TrafficPercentExplicit {
		return true, Deployment{}, ErrConflict
	}
	if existing, ok := m.deployments[d.ID]; ok {
		if existing.AppID != d.AppID || existing.Scope != d.Scope || existing.Reason != d.Reason || existing.ImageDigest != d.ImageDigest {
			return true, Deployment{}, ErrConflict
		}
		return true, existing, nil
	}
	return true, Deployment{}, nil
}
func bindingProjectPromotionDeploymentTx(ctx context.Context, tx pgx.Tx, d Deployment) (bool, error) {
	id := bindingProjectPromotionReasonID(d.Reason)
	if id == "" {
		return false, nil
	}
	row, err := sqlc.New().ReadBindingProjectPromotionDeploymentIntent(ctx, tx, sqlc.ReadBindingProjectPromotionDeploymentIntentParams{PromotionID: mustPgUUID(id), AppID: mustPgUUID(d.AppID)})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, mapErr(err)
	}
	if !row.BindingsRequired {
		return false, nil
	}
	claim := bindingProjectPromotionClaim(ctx)
	if !sameDeploymentID(claim.PromotionID, id) || claim.AccountID != uuidString(row.AccountID) {
		return true, ErrBindingProjectPromotionLease
	}
	if err := lockBindingProjectPromotionClaim(ctx, tx, claim); err != nil {
		return true, err
	}
	if row.Status != "pending" || !sameDeploymentID(d.ID, row.WTargetDeploymentID) || d.ID == "" || d.Scope != row.ToEnvironment || d.TrafficPercent != 0 || !d.TrafficPercentExplicit {
		return true, ErrConflict
	}
	return true, nil
}

func bindingProjectPromotionMutationTx(ctx context.Context, tx pgx.Tx, id string) (bool, error) {
	d, err := deploymentByIDDB(ctx, tx, id)
	if err != nil {
		return false, err
	}
	return bindingProjectPromotionDeploymentTx(ctx, tx, d)
}
func bindingProjectPromotionMutationDeadline(ctx context.Context, checked bool) error {
	if checked && !bindingProjectPromotionClaim(ctx).Until.After(time.Now()) {
		return ErrBindingProjectPromotionLease
	}
	return nil
}
