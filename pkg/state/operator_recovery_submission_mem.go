// adr: 1524
package state

import (
	"context"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

var _ OperatorRecoverySubmissionStore = (*MemStore)(nil)

type operatorRecoverySubmissionRow struct {
	receipt OperatorRecoveryReceipt
	digest  string
}

var operatorRecoveryLegacyPath = regexp.MustCompile(`^POST /v1/admin/(instances/[^/]+/force-(park|restart)|apps/[^/]+/force-cold-boot)$`)

func (m *MemStore) SubmitOperatorRecovery(ctx context.Context, in OperatorRecoverySubmission) (OperatorRecoveryReceipt, error) {
	in, err := NormalizeOperatorRecovery(in)
	if err != nil {
		return OperatorRecoveryReceipt{}, err
	}
	key, digest := operatorRecoveryDigests(in)
	key = in.ActorID + ":" + key
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return OperatorRecoveryReceipt{}, err
	}
	if row, ok := m.operatorRecoverySubmissions[key]; ok {
		return replayOperatorRecovery(row.receipt, row.digest, digest)
	}
	for legacy := range m.idem {
		actor, scoped, _ := strings.Cut(legacy, "\x00")
		path, requestKey, _ := strings.Cut(scoped, "\n")
		if sameRestartID(actor, in.ActorID) && requestKey == in.RequestKey && operatorRecoveryLegacyPath.MatchString(path) {
			return OperatorRecoveryReceipt{}, ErrOperatorRecoveryLegacyKey
		}
	}
	target, err := m.operatorRecoveryTargetLocked(in)
	if err != nil {
		return OperatorRecoveryReceipt{}, err
	}
	if err := operatorRecoveryEligible(in, target); err != nil {
		return OperatorRecoveryReceipt{}, err
	}
	now := time.Now().UTC()
	receipt, audit, err := newOperatorRecoveryReceipt(in, target, now)
	if err != nil {
		return receipt, err
	}
	if err := validateAuditEventOutboxInput("apid", OperatorRecoveryAuditKind(in.Kind), audit, "operator-recovery-request:"+receipt.IntentID); err != nil {
		return OperatorRecoveryReceipt{}, err
	}
	if err := ctx.Err(); err != nil {
		return OperatorRecoveryReceipt{}, err
	}
	// No fallible operations after the audit handoff is appended under this lock.
	receipt.AuditOutboxID, _ = m.enqueueAuditEventLocked("apid", OperatorRecoveryAuditKind(in.Kind), &target.AccountID, audit, "operator-recovery-request:"+receipt.IntentID)
	if m.operatorIntents == nil {
		m.operatorIntents = make(map[string]OperatorIntent)
	}
	for _, existing := range m.operatorIntents {
		if !existing.RequestedAt.Before(now) {
			now = existing.RequestedAt.Add(time.Nanosecond)
		}
	}
	m.operatorIntents[receipt.IntentID] = OperatorIntent{ID: receipt.IntentID, Kind: in.Kind, TargetID: receipt.TargetID,
		AccountID: cloneStringPtr(&target.AccountID), ActorID: in.ActorID, Reason: in.Reason, Metadata: []byte("{}"),
		Status: OperatorIntentPending, RequestedAt: now, TraceID: cloneStringPtr(in.TraceID)}
	if m.operatorRecoverySubmissions == nil {
		m.operatorRecoverySubmissions = make(map[string]operatorRecoverySubmissionRow)
	}
	m.operatorRecoverySubmissions[key] = operatorRecoverySubmissionRow{receipt: receipt, digest: digest}
	receipt.ResponseBody = append([]byte(nil), receipt.ResponseBody...)
	return receipt, nil
}

func memRecoveryRow[T any](rows map[string]T, id string) (T, bool) {
	row, ok := rows[id]
	if !ok {
		if parsed, err := uuid.Parse(id); err == nil {
			row, ok = rows[strings.ReplaceAll(parsed.String(), "-", "")]
		}
	}
	return row, ok
}

func (m *MemStore) operatorRecoveryTargetLocked(in OperatorRecoverySubmission) (operatorRecoveryTarget, error) {
	var app App
	var target operatorRecoveryTarget
	if in.Kind == OperatorIntentKindForceColdBoot {
		for _, candidate := range m.apps {
			if candidate.Slug == in.Target && candidate.Status != AppDeleted {
				app = candidate
				break
			}
		}
		if app.ID == "" {
			return target, ErrNotFound
		}
		var latest Deployment
		for _, d := range m.deployments {
			if d.AppID == app.ID && (latest.ID == "" || d.CreatedAt.After(latest.CreatedAt) || d.CreatedAt.Equal(latest.CreatedAt) && d.ID > latest.ID) {
				latest = d
			}
		}
		if latest.ID == "" {
			return target, ErrOperatorRecoveryDeployment
		}
		target.DeploymentID = latest.ID
	} else {
		instance, ok := memRecoveryRow(m.instances, in.Target)
		if !ok {
			return target, ErrNotFound
		}
		app, ok = memRecoveryRow(m.apps, instance.AppID)
		if !ok || app.Status == AppDeleted {
			return target, ErrNotFound
		}
		target.InstanceID, target.DeploymentID, target.State = instance.ID, instance.DeploymentID, instance.State
	}
	owner, err := uuid.Parse(app.AccountID)
	if err != nil || owner == uuid.Nil {
		return target, ErrNotFound
	}
	target.AppID, target.AccountID = app.ID, app.AccountID
	return target, nil
}
