package state

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/exclusivework"
	"github.com/onebox-faas/faas/pkg/workpolicy"
)

type ExclusiveWorkPolicy struct {
	ID        string               `json:"id"`
	AccountID string               `json:"-"`
	Revision  int64                `json:"revision"`
	Policy    exclusivework.Policy `json:"policy"`
	Retired   bool                 `json:"retired"`
	CreatedAt time.Time            `json:"created_at"`
	UpdatedAt time.Time            `json:"updated_at"`
}

type ExclusiveOperation struct {
	ID                string               `json:"id"`
	AccountID         string               `json:"-"`
	KeyID             string               `json:"-"`
	AppID             string               `json:"app_id,omitempty"`
	JobID             string               `json:"job_id,omitempty"`
	PlatformTenantID  string               `json:"platform_tenant_id,omitempty"`
	Sequence          int64                `json:"sequence"`
	State             string               `json:"state"`
	PolicyRevision    int64                `json:"policy_revision"`
	Policy            exclusivework.Policy `json:"-"`
	Request           json.RawMessage      `json:"-"`
	RequestDigest     []byte               `json:"-"`
	EquivalenceDigest []byte               `json:"-"`
	IdempotencyDigest []byte               `json:"-"`
	Generation        int64                `json:"generation"`
	ClaimToken        string               `json:"-"`
	IncarnationID     string               `json:"-"`
	LeaseExpiresAt    *time.Time           `json:"lease_expires_at,omitempty"`
	AttemptDeadline   *time.Time           `json:"attempt_deadline,omitempty"`
	Result            json.RawMessage      `json:"result,omitempty"`
	LastError         string               `json:"last_error,omitempty"`
	CreatedAt         time.Time            `json:"created_at"`
	DueAt             time.Time            `json:"due_at"`
	Attempts          int                  `json:"attempts"`
	QuotaReserved     bool                 `json:"-"`
	// Replayed marks a receipt returned for an already accepted idempotency
	// identity. It is not persisted and is distinct from joining active work.
	Replayed    bool       `json:"-"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
}

// Admission receives identity from authenticated platform context, never from
// the key. The request is a normalized dispatch description including revision.
type ExclusiveAdmission struct {
	AccountID, AppID, JobID, PlatformTenantID, PolicyName string
	Key                                                   json.RawMessage
	Request                                               json.RawMessage
	EquivalenceKey, IdempotencyKey                        string
}

type ExclusiveWorkStore interface {
	UpsertExclusiveWorkPolicy(context.Context, string, exclusivework.Policy) (ExclusiveWorkPolicy, error)
	ListExclusiveWorkPolicies(context.Context, string) ([]ExclusiveWorkPolicy, error)
	AdmitExclusiveOperation(context.Context, ExclusiveAdmission) (ExclusiveOperation, bool, error)
	ExclusiveOperationByID(context.Context, string, string) (ExclusiveOperation, error)
	ClaimExclusiveOperation(context.Context, string, string, string) (exclusivework.Claim, error)
	RenewExclusiveOperation(context.Context, exclusivework.Claim) (exclusivework.Claim, error)
	CommitExclusiveOperation(context.Context, exclusivework.Claim, json.RawMessage, []exclusivework.Effect) error
	RetryExclusiveOperation(context.Context, exclusivework.Claim, string) error
	CancelExclusiveOperation(context.Context, string, string) error
	ValidateExclusiveOperation(context.Context, exclusivework.Claim) (ExclusiveOperation, error)
	FailExclusiveOperation(context.Context, exclusivework.Claim, string) error
	FailPendingExclusiveOperation(context.Context, string, string, string) error
	ListDueExclusiveOperations(context.Context, int) ([]ExclusiveOperation, error)
	DeferPendingExclusiveOperation(context.Context, string, string, string) error
}

// Only schedd acquires this barrier. It covers warm and init capture, including
// the interval when a warm snapshot resumes the guest before publication.
type ExclusiveSnapshotStore interface {
	BeginExclusiveSnapshot(context.Context, string) error
	EndExclusiveSnapshot(context.Context, string) error
}

// ExclusiveTriggerBinding records which customer-configured trigger should
// submit work to an operation lane. Trigger identity is resolved from storage;
// account and tenant scope are validated independently from the business key.
type ExclusiveTriggerBinding struct {
	Source           string          `json:"source"`
	TriggerID        string          `json:"trigger_id"`
	AccountID        string          `json:"-"`
	AppID            string          `json:"app_id,omitempty"`
	JobID            string          `json:"job_id,omitempty"`
	PolicyName       string          `json:"policy"`
	PlatformTenantID string          `json:"platform_tenant_id,omitempty"`
	Key              json.RawMessage `json:"key"`
	EquivalenceKey   string          `json:"equivalence_key,omitempty"`
	CreatedAt        time.Time       `json:"created_at"`
	UpdatedAt        time.Time       `json:"updated_at"`
}

type ExclusiveTriggerBindingStore interface {
	UpsertExclusiveTriggerBinding(context.Context, ExclusiveTriggerBinding) (ExclusiveTriggerBinding, error)
	ExclusiveTriggerBinding(context.Context, string, string, string) (ExclusiveTriggerBinding, error)
	DeleteExclusiveTriggerBinding(context.Context, string, string, string) error
}

// ExclusiveTriggerRecordLinker stores the accepted operation receipt in the
// claimed trigger record before the broker delivery is acknowledged.
type ExclusiveTriggerRecordLinker interface {
	LinkExclusiveTriggerRecordOperation(context.Context, string, int64, string) error
}

type exclusiveKey struct {
	ID, AccountID, PolicyID, ScopeID, EnvironmentID string
	Digest                                          []byte
	Generation, NextSequence                        int64
}

// Both stores execute the same transitions; transaction implementations supply
// database row locks or the MemStore mutex. PostgreSQL time is sampled after locks.
type exclusiveTransaction interface {
	lockAccount(string) error
	appScope(string, string) (string, error)
	jobScope(string, string) error
	jobRunScope(string, string, int64) error
	appTaskScope(string, string, int64) error
	tenantScope(string, string) error
	environmentScope(string, string, string) error
	runtimeScope(string, string, string, string, string) error
	reserve(string) error
	now() (time.Time, error)
	policy(string, string) (ExclusiveWorkPolicy, error)
	policies(string) ([]ExclusiveWorkPolicy, error)
	savePolicy(ExclusiveWorkPolicy) (ExclusiveWorkPolicy, error)
	ensureKey(exclusiveKey) (exclusiveKey, error)
	key(string, string) (exclusiveKey, error)
	saveKey(exclusiveKey) error
	operation(string, string) (ExclusiveOperation, error)
	replay(string, []byte) (ExclusiveOperation, error)
	bindSubmission(string, []byte, string) error
	active(string) ([]ExclusiveOperation, error)
	pendingCount(string) (int64, error)
	insert(ExclusiveOperation) (ExclusiveOperation, error)
	save(ExclusiveOperation) error
	effects(ExclusiveOperation, []exclusivework.Effect) error
}

type exclusiveAtomic func(context.Context, func(exclusiveTransaction) error) error

func validateExclusivePolicy(p *exclusivework.Policy) error {
	if p.LeaseSeconds == 0 {
		p.LeaseSeconds = api.DefaultExclusiveLeaseSeconds
	}
	if p.MaxAttemptSeconds == 0 {
		p.MaxAttemptSeconds = p.LeaseSeconds * 10
	}
	if p.MaxAttempts == 0 {
		p.MaxAttempts = api.DefaultExclusiveAttempts
	}
	if p.RetryAfterSeconds == 0 {
		p.RetryAfterSeconds = api.DefaultExclusiveRetrySeconds
	}
	if !exclusivework.NamePattern.MatchString(p.Name) ||
		(p.Scope != "account" && p.Scope != "platform_tenant") ||
		(p.Contention != "queue" && p.Contention != "reject" && p.Contention != "join_existing") ||
		p.LeaseSeconds < api.MinExclusiveLeaseSeconds || p.LeaseSeconds > api.MaxExclusiveLeaseSeconds ||
		p.MaxAttemptSeconds < p.LeaseSeconds || p.MaxAttemptSeconds > api.MaxExclusiveAttemptSeconds ||
		(len(p.MemberAppIDs)+len(p.MemberJobIDs)) == 0 || (len(p.MemberAppIDs)+len(p.MemberJobIDs)) > api.MaxExclusiveMembers ||
		p.MaxAttempts < 1 || p.MaxAttempts > api.MaxExclusiveAttempts ||
		p.RetryAfterSeconds < 1 || p.RetryAfterSeconds > api.MaxExclusiveRetrySeconds {
		return fmt.Errorf("%w: invalid exclusive operation policy", ErrInvalidArgument)
	}
	if p.EnvironmentID != "" {
		if _, err := uuid.Parse(p.EnvironmentID); err != nil {
			return ErrInvalidArgument
		}
	}
	if p.EnvironmentID != "" && len(p.MemberJobIDs) > 0 {
		return fmt.Errorf("%w: job members cannot use an app project environment", ErrInvalidArgument)
	}
	if p.Scope == "platform_tenant" && len(p.MemberJobIDs) > 0 {
		return fmt.Errorf("%w: Job members require account scope", ErrInvalidArgument)
	}
	p.MemberAppIDs = slices.Clone(p.MemberAppIDs)
	slices.Sort(p.MemberAppIDs)
	for i, id := range p.MemberAppIDs {
		if _, err := uuid.Parse(id); err != nil || (i > 0 && p.MemberAppIDs[i-1] == id) {
			return ErrInvalidArgument
		}
	}
	p.MemberJobIDs = slices.Clone(p.MemberJobIDs)
	slices.Sort(p.MemberJobIDs)
	for i, id := range p.MemberJobIDs {
		if _, err := uuid.Parse(id); err != nil || (i > 0 && p.MemberJobIDs[i-1] == id) {
			return ErrInvalidArgument
		}
	}
	return nil
}

func upsertExclusivePolicy(ctx context.Context, atomic exclusiveAtomic, account string, p exclusivework.Policy) (out ExclusiveWorkPolicy, err error) {
	if err = validateExclusivePolicy(&p); err != nil {
		return
	}
	err = atomic(ctx, func(tx exclusiveTransaction) error {
		if err := tx.lockAccount(account); err != nil {
			return err
		}
		for _, app := range p.MemberAppIDs {
			project, err := tx.appScope(account, app)
			if err != nil {
				return err
			}
			if p.EnvironmentID != "" {
				if err := tx.environmentScope(account, project, p.EnvironmentID); err != nil {
					return err
				}
			}
		}
		for _, job := range p.MemberJobIDs {
			if err := tx.jobScope(account, job); err != nil {
				return err
			}
		}
		old, err := tx.policy(account, p.Name)
		if err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}
		if old.Retired {
			return ErrConflict
		}
		if old.ID != "" && (old.Policy.Scope != p.Scope || old.Policy.EnvironmentID != p.EnvironmentID) {
			return ErrConflict
		}
		if old.ID == "" {
			policies, err := tx.policies(account)
			if err != nil {
				return err
			}
			if len(policies) >= api.MaxExclusivePoliciesPerAccount {
				return ErrQuotaExceeded
			}
			old = ExclusiveWorkPolicy{ID: uuid.NewString(), AccountID: account}
		}
		old.Policy = p
		out, err = tx.savePolicy(old)
		return err
	})
	return
}

func canonicalExclusiveJSON(raw json.RawMessage) ([]byte, error) {
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, ErrInvalidArgument
	}
	if _, ok := value.(map[string]any); !ok {
		return nil, ErrInvalidArgument
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, ErrInvalidArgument
	}
	return json.Marshal(value)
}

func exclusiveIdentityDigest(value string) []byte {
	if value == "" {
		return nil
	}
	hash := sha256.Sum256([]byte(value))
	return hash[:]
}

func admitExclusive(ctx context.Context, atomic exclusiveAtomic, req ExclusiveAdmission) (out ExclusiveOperation, joined bool, err error) {
	if (req.AppID == "") == (req.JobID == "") {
		return out, false, ErrInvalidArgument
	}
	canonicalKey, err := workpolicy.CanonicalScalar(req.Key)
	if err != nil {
		return out, false, ErrInvalidArgument
	}
	keyDigest, err := workpolicy.DigestKey(canonicalKey)
	if err != nil {
		return out, false, err
	}
	request, err := canonicalExclusiveJSON(req.Request)
	if err != nil {
		return out, false, err
	}
	if len(request) > api.MaxExclusiveRequestBytes {
		return out, false, ErrInvalidArgument
	}
	if len(req.EquivalenceKey) > api.MaxExclusiveIdentityBytes || len(req.IdempotencyKey) > api.MaxExclusiveIdentityBytes {
		return out, false, ErrInvalidArgument
	}
	target := "app:" + req.AppID
	if req.JobID != "" {
		target = "job:" + req.JobID
	}
	requestHash := sha256.Sum256(append([]byte(target+"\x00"), request...))
	err = atomic(ctx, func(tx exclusiveTransaction) error {
		if err := tx.lockAccount(req.AccountID); err != nil {
			return err
		}
		p, err := tx.policy(req.AccountID, req.PolicyName)
		if err != nil {
			return err
		}
		if p.Retired || (req.AppID != "" && !slices.Contains(p.Policy.MemberAppIDs, req.AppID)) ||
			(req.JobID != "" && !slices.Contains(p.Policy.MemberJobIDs, req.JobID)) {
			return ErrNotFound
		}
		if req.AppID != "" {
			project, err := tx.appScope(req.AccountID, req.AppID)
			if err != nil {
				return err
			}
			if p.Policy.EnvironmentID != "" {
				if err := tx.environmentScope(req.AccountID, project, p.Policy.EnvironmentID); err != nil {
					return err
				}
			}
		} else {
			if p.Policy.EnvironmentID != "" {
				return ErrInvalidArgument
			}
			if err := tx.jobScope(req.AccountID, req.JobID); err != nil {
				return err
			}
		}
		scopeID := req.AccountID
		if p.Policy.Scope == "platform_tenant" {
			if req.PlatformTenantID == "" {
				return ErrInvalidArgument
			}
			if err := tx.tenantScope(req.AccountID, req.PlatformTenantID); err != nil {
				return err
			}
			scopeID = req.PlatformTenantID
		} else if req.PlatformTenantID != "" {
			return ErrInvalidArgument
		}
		key, err := tx.ensureKey(exclusiveKey{ID: uuid.NewString(), AccountID: req.AccountID,
			PolicyID: p.ID, ScopeID: scopeID, EnvironmentID: p.Policy.EnvironmentID, Digest: keyDigest[:]})
		if err != nil {
			return err
		}
		idem := exclusiveIdentityDigest(req.IdempotencyKey)
		if idem != nil {
			out, err = tx.replay(key.ID, idem)
			if err == nil {
				if !bytes.Equal(out.RequestDigest, requestHash[:]) {
					return exclusivework.ErrIdentityConflict
				}
				out.Replayed = true
				return nil
			}
			if !errors.Is(err, ErrNotFound) {
				return err
			}
		}
		active, err := tx.active(key.ID)
		if err != nil {
			return err
		}
		equiv := exclusiveIdentityDigest(req.EquivalenceKey)
		if p.Policy.Contention == "join_existing" && equiv == nil {
			return ErrInvalidArgument
		}
		if len(active) > 0 && p.Policy.Contention != "queue" {
			if p.Policy.Contention == "join_existing" {
				for _, row := range active {
					if bytes.Equal(equiv, row.EquivalenceDigest) && bytes.Equal(row.RequestDigest, requestHash[:]) {
						out, joined = row, true
						return tx.bindSubmission(key.ID, idem, row.ID)
					}
				}
			}
			return exclusivework.ErrBusy
		}
		count, err := tx.pendingCount(req.AccountID)
		if err != nil {
			return err
		}
		if count >= api.MaxExclusivePendingPerAccount {
			return ErrQuotaExceeded
		}
		out, err = tx.insert(ExclusiveOperation{ID: uuid.NewString(), AccountID: req.AccountID,
			KeyID: key.ID, AppID: req.AppID, JobID: req.JobID, PlatformTenantID: req.PlatformTenantID,
			Sequence: key.NextSequence, State: "pending", PolicyRevision: p.Revision, Policy: p.Policy,
			Request: request, RequestDigest: requestHash[:], EquivalenceDigest: equiv, IdempotencyDigest: idem})
		if err != nil {
			return err
		}
		if err := tx.bindSubmission(key.ID, idem, out.ID); err != nil {
			return err
		}
		key.NextSequence++
		return tx.saveKey(key)
	})
	return
}

func exclusiveClaimFor(op ExclusiveOperation) exclusivework.Claim {
	return exclusivework.Claim{OperationID: op.ID, AccountID: op.AccountID,
		Generation: op.Generation, Token: op.ClaimToken, IncarnationID: op.IncarnationID,
		ExpiresAt: *op.LeaseExpiresAt, Deadline: *op.AttemptDeadline}
}

func clearExclusiveClaim(op *ExclusiveOperation) {
	op.ClaimToken = ""
	op.LeaseExpiresAt, op.AttemptDeadline = nil, nil
}

func claimExclusive(ctx context.Context, atomic exclusiveAtomic, account, id, incarnation string) (out exclusivework.Claim, err error) {
	if incarnation == "" || len(incarnation) > api.MaxExclusiveIdentityBytes {
		return out, ErrInvalidArgument
	}
	var transitionErr error
	err = atomic(ctx, func(tx exclusiveTransaction) error {
		if err := tx.lockAccount(account); err != nil {
			return err
		}
		op, err := tx.operation(account, id)
		if err != nil {
			return err
		}
		key, err := tx.key(account, op.KeyID)
		if err != nil {
			return err
		}
		if op.PlatformTenantID != "" {
			if err := tx.tenantScope(account, op.PlatformTenantID); err != nil {
				return err
			}
		}
		rows, err := tx.active(key.ID)
		if err != nil {
			return err
		}
		now, err := tx.now()
		if err != nil {
			return err
		}
		// Expiration invalidates authority even before a successor is granted.
		// Recovery stays committed when this particular contender is not FIFO head.
		for i := range rows {
			row := &rows[i]
			if row.State != "running" || row.LeaseExpiresAt.After(now) {
				continue
			}
			row.State, row.LastError = "pending", "ownership lease expired"
			if !row.AttemptDeadline.After(now) {
				row.State = "failed"
				row.CompletedAt = &now
			}
			clearExclusiveClaim(row)
			if err := tx.save(*row); err != nil {
				return err
			}
		}
		var head *ExclusiveOperation
		for i := range rows {
			if rows[i].State == "pending" || rows[i].State == "running" {
				head = &rows[i]
				break
			}
		}
		if head == nil || head.ID != id || head.State != "pending" || head.DueAt.After(now) {
			transitionErr = exclusivework.ErrBusy
			return nil
		}
		op = *head
		if op.Attempts >= op.Policy.MaxAttempts {
			op.State, op.LastError, op.CompletedAt = "failed", "attempt budget exhausted", &now
			transitionErr = exclusivework.ErrBusy
			return tx.save(op)
		}
		if err := validateExclusiveClaimTarget(tx, op, incarnation, false); err != nil {
			return err
		}
		if key.Generation == math.MaxInt64 {
			return ErrQuotaExceeded
		}
		if err := tx.reserve(account); err != nil {
			return err
		}
		key.Generation++
		op.Attempts++
		op.QuotaReserved = true
		op.Generation, op.ClaimToken, op.IncarnationID = key.Generation, uuid.NewString(), incarnation
		op.State = "running"
		deadline := now.Add(time.Duration(op.Policy.MaxAttemptSeconds) * time.Second)
		expires := now.Add(time.Duration(op.Policy.LeaseSeconds) * time.Second)
		op.LeaseExpiresAt, op.AttemptDeadline = &expires, &deadline
		if err := tx.saveKey(key); err != nil {
			return err
		}
		if err := tx.save(op); err != nil {
			return err
		}
		out = exclusiveClaimFor(op)
		return nil
	})
	if err == nil {
		err = transitionErr
	}
	return
}

// validateExclusiveOwner is always called under the same transaction as the
// effect/result write. Expired capabilities are rejected without needing a reaper.
func validateExclusiveOwner(tx exclusiveTransaction, claim exclusivework.Claim) (ExclusiveOperation, time.Time, error) {
	if claim.Token == "" || claim.Generation <= 0 || claim.IncarnationID == "" {
		return ExclusiveOperation{}, time.Time{}, exclusivework.ErrStaleOwner
	}
	if err := tx.lockAccount(claim.AccountID); err != nil {
		return ExclusiveOperation{}, time.Time{}, err
	}
	op, err := tx.operation(claim.AccountID, claim.OperationID)
	if err != nil {
		return op, time.Time{}, err
	}
	key, err := tx.key(claim.AccountID, op.KeyID)
	if err != nil {
		return op, time.Time{}, err
	}
	now, err := tx.now()
	if err != nil {
		return op, now, err
	}
	if op.State != "running" || op.Generation != claim.Generation || key.Generation != claim.Generation ||
		op.ClaimToken != claim.Token || op.IncarnationID != claim.IncarnationID ||
		op.LeaseExpiresAt == nil || !op.LeaseExpiresAt.After(now) ||
		op.AttemptDeadline == nil || !op.AttemptDeadline.After(now) {
		return op, now, exclusivework.ErrStaleOwner
	}
	if err := validateExclusiveClaimTarget(tx, op, claim.IncarnationID, true); err != nil {
		return op, now, err
	}
	if op.PlatformTenantID != "" {
		if err := tx.tenantScope(op.AccountID, op.PlatformTenantID); err != nil {
			return op, now, err
		}
	}
	return op, now, nil
}

func validateExclusiveClaimTarget(tx exclusiveTransaction, op ExclusiveOperation, incarnation string, claimed bool) error {
	if op.JobID != "" {
		if !claimed {
			return nil
		}
		return tx.jobRunScope(op.AccountID, op.ID, op.Generation)
	}
	var requestKind struct {
		Kind string `json:"kind"`
	}
	if json.Unmarshal(op.Request, &requestKind) == nil && requestKind.Kind == "app_task" {
		if !claimed {
			return nil
		}
		return tx.appTaskScope(op.AccountID, op.ID, op.Generation)
	}
	parts := strings.Split(incarnation, "/")
	if len(parts) != 3 {
		return exclusivework.ErrStaleOwner
	}
	return tx.runtimeScope(op.AccountID, op.AppID, parts[0], parts[1], parts[2])
}

func validateExclusive(ctx context.Context, atomic exclusiveAtomic, claim exclusivework.Claim) (out ExclusiveOperation, err error) {
	err = atomic(ctx, func(tx exclusiveTransaction) error {
		var e error
		out, _, e = validateExclusiveOwner(tx, claim)
		return e
	})
	return
}

func renewExclusive(ctx context.Context, atomic exclusiveAtomic, claim exclusivework.Claim) (out exclusivework.Claim, err error) {
	err = atomic(ctx, func(tx exclusiveTransaction) error {
		op, now, err := validateExclusiveOwner(tx, claim)
		if err != nil {
			return err
		}
		expires := now.Add(time.Duration(op.Policy.LeaseSeconds) * time.Second)
		if expires.After(*op.AttemptDeadline) {
			expires = *op.AttemptDeadline
		}
		op.LeaseExpiresAt = &expires
		if err := tx.save(op); err != nil {
			return err
		}
		out = exclusiveClaimFor(op)
		return nil
	})
	return
}

func commitExclusive(ctx context.Context, atomic exclusiveAtomic, claim exclusivework.Claim, result json.RawMessage, effects []exclusivework.Effect) error {
	if len(result) > api.MaxExclusiveResultBytes || (len(result) > 0 && !json.Valid(result)) {
		return ErrInvalidArgument
	}
	if len(effects) > api.MaxExclusiveEffectsPerCommit {
		return ErrInvalidArgument
	}
	names := make(map[string]bool, len(effects))
	for _, effect := range effects {
		if !exclusivework.NamePattern.MatchString(effect.Name) || names[effect.Name] || len(effect.Payload) > 64<<10 || !json.Valid(effect.Payload) {
			return ErrInvalidArgument
		}
		names[effect.Name] = true
	}
	return atomic(ctx, func(tx exclusiveTransaction) error {
		op, now, err := validateExclusiveOwner(tx, claim)
		if err != nil {
			return err
		}
		if err := tx.effects(op, effects); err != nil {
			return err
		}
		op.State, op.Result, op.CompletedAt = "completed", slices.Clone(result), &now
		clearExclusiveClaim(&op)
		return tx.save(op)
	})
}

func finishExclusiveFailure(ctx context.Context, atomic exclusiveAtomic, claim exclusivework.Claim, reason string, retry bool) error {
	if len(reason) > api.MaxExclusiveErrorBytes {
		reason = reason[:api.MaxExclusiveErrorBytes]
	}
	return atomic(ctx, func(tx exclusiveTransaction) error {
		op, now, err := validateExclusiveOwner(tx, claim)
		if err != nil {
			return err
		}
		op.State, op.LastError = "pending", reason
		op.DueAt = now.Add(time.Duration(op.Policy.RetryAfterSeconds) * time.Second)
		if !retry || op.Attempts >= op.Policy.MaxAttempts {
			op.State, op.CompletedAt = "failed", &now
		}
		clearExclusiveClaim(&op)
		return tx.save(op)
	})
}

func retryExclusive(ctx context.Context, atomic exclusiveAtomic, claim exclusivework.Claim, reason string) error {
	return finishExclusiveFailure(ctx, atomic, claim, reason, true)
}

func failPendingExclusive(ctx context.Context, atomic exclusiveAtomic, account, id, reason string) error {
	if len(reason) > api.MaxExclusiveErrorBytes {
		reason = reason[:api.MaxExclusiveErrorBytes]
	}
	return atomic(ctx, func(tx exclusiveTransaction) error {
		if err := tx.lockAccount(account); err != nil {
			return err
		}
		op, err := tx.operation(account, id)
		if err != nil {
			return err
		}
		if _, err := tx.key(account, op.KeyID); err != nil {
			return err
		}
		if op.State != "pending" {
			return exclusivework.ErrBusy
		}
		now, err := tx.now()
		if err != nil {
			return err
		}
		op.State, op.LastError, op.CompletedAt = "failed", reason, &now
		return tx.save(op)
	})
}

func deferPendingExclusive(ctx context.Context, atomic exclusiveAtomic, account, id, reason string) error {
	if len(reason) > api.MaxExclusiveErrorBytes {
		reason = reason[:api.MaxExclusiveErrorBytes]
	}
	return atomic(ctx, func(tx exclusiveTransaction) error {
		if err := tx.lockAccount(account); err != nil {
			return err
		}
		op, err := tx.operation(account, id)
		if err != nil {
			return err
		}
		if _, err := tx.key(account, op.KeyID); err != nil {
			return err
		}
		if op.State != "pending" {
			return exclusivework.ErrBusy
		}
		now, err := tx.now()
		if err != nil {
			return err
		}
		op.DueAt = now.Add(time.Duration(op.Policy.RetryAfterSeconds) * time.Second)
		op.LastError = reason
		return tx.save(op)
	})
}

func cancelExclusive(ctx context.Context, atomic exclusiveAtomic, account, id string) error {
	return atomic(ctx, func(tx exclusiveTransaction) error {
		if err := tx.lockAccount(account); err != nil {
			return err
		}
		op, err := tx.operation(account, id)
		if err != nil {
			return err
		}
		if _, err := tx.key(account, op.KeyID); err != nil {
			return err
		}
		if op.State != "pending" && op.State != "running" {
			return nil
		}
		now, err := tx.now()
		if err != nil {
			return err
		}
		op.State, op.CompletedAt = "cancelled", &now
		clearExclusiveClaim(&op)
		return tx.save(op)
	})
}
