package state

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/api"
	grantsql "github.com/onebox-faas/faas/pkg/state/supportgrants/sqlc"
)

// Customer-granted support access (ADR-20261116163508). An owner or admin
// lets Gregale support deploy, roll back and restart the organization's apps
// for a while; support may ask first with a request.

// Limits live in pkg/api/limits.go; the migration's CHECKs mirror them.
const (
	SupportGrantMaxTTL          = api.SupportGrantMaxTTL
	SupportGrantRequestTTL      = api.SupportGrantRequestTTL
	SupportGrantMaxOpenRequests = api.SupportGrantMaxOpenRequests
	SupportGrantMaxApps         = api.SupportGrantMaxApps
)

// Request statuses.
const (
	SupportRequestOpen     = "open"
	SupportRequestApproved = "approved"
	SupportRequestDeclined = "declined"
)

// ErrSupportRequestLimit means the organization already has the maximum
// number of open requests.
var ErrSupportRequestLimit = errors.New("state: too many open support access requests")

// SupportGrant lets Gregale support act on an organization's apps. AppIDs
// empty means every app of the organization.
type SupportGrant struct {
	ID        string
	OrgID     string
	OrgSlug   string
	AppIDs    []string
	CreatedBy string
	RequestID string
	Reason    string
	CreatedAt time.Time
	ExpiresAt time.Time
	RevokedAt *time.Time
	RevokedBy string
}

// Live reports whether the grant is unrevoked and unexpired at now. It does
// not check its creator's role; UsableSupportGrant does.
func (g SupportGrant) Live(now time.Time) bool {
	return g.RevokedAt == nil && g.ExpiresAt.After(now)
}

// Covers reports whether the grant reaches appID.
func (g SupportGrant) Covers(appID string) bool {
	return len(g.AppIDs) == 0 || slices.Contains(g.AppIDs, appID)
}

// SupportGrantRequest is an operator's request for a grant. AppIDs and TTL
// are what support asks for (empty and zero leave the choice to the
// customer); ExtendsGrantID names the grant a request to extend continues.
type SupportGrantRequest struct {
	ID             string
	OrgID          string
	OperatorID     string
	Reason         string
	Status         string
	CreatedAt      time.Time
	ExpiresAt      time.Time
	DecidedAt      *time.Time
	DecidedBy      string
	AppIDs         []string
	TTL            time.Duration
	ExtendsGrantID string
}

// NewSupportGrantRequest is what an operator asks for.
type NewSupportGrantRequest struct {
	OrgID          string
	OperatorID     string
	Reason         string
	AppIDs         []string
	TTL            time.Duration
	ExtendsGrantID string
}

// NewSupportGrant is what a customer grants.
type NewSupportGrant struct {
	OrgID     string
	AppIDs    []string
	CreatedBy string
	RequestID string
	Reason    string
	TTL       time.Duration
}

// SupportGrantStore is implemented by MemStore and PgStore.
type SupportGrantStore interface {
	CreateSupportGrant(ctx context.Context, grant NewSupportGrant) (SupportGrant, error)
	SupportGrantByID(ctx context.Context, id string) (SupportGrant, error)
	// UsableSupportGrant returns the grant only while it is live and its
	// creator is still an active owner or admin; otherwise ErrNotFound.
	UsableSupportGrant(ctx context.Context, id string) (SupportGrant, error)
	ListSupportGrants(ctx context.Context, orgID string) ([]SupportGrant, error)
	ListLiveSupportGrants(ctx context.Context) ([]SupportGrant, error)
	RevokeSupportGrant(ctx context.Context, orgID, id, revokedBy string) error
	CreateSupportGrantRequest(ctx context.Context, req NewSupportGrantRequest) (SupportGrantRequest, error)
	ListSupportGrantRequests(ctx context.Context, orgID string) ([]SupportGrantRequest, error)
	// ApproveSupportGrantRequest decides an open request and creates its
	// grant atomically.
	ApproveSupportGrantRequest(ctx context.Context, requestID string, grant NewSupportGrant) (SupportGrant, error)
	DeclineSupportGrantRequest(ctx context.Context, orgID, requestID, decidedBy string) error
	// ClaimEndedSupportGrants marks up to limit grants that ended in the
	// last day as summarized and returns them, each exactly once.
	ClaimEndedSupportGrants(ctx context.Context, limit int) ([]SupportGrant, error)
}

var (
	_ SupportGrantStore = (*MemStore)(nil)
	_ SupportGrantStore = (*PgStore)(nil)
)

// ValidSupportReason reports whether reason has an allowed length in
// characters, the unit of the tables' char_length CHECKs.
func ValidSupportReason(reason string) bool {
	n := utf8.RuneCountInString(reason)
	return n >= api.SupportReasonMinChars && n <= api.SupportReasonMaxChars
}

// ValidSupportGrantRequest checks a request before it is stored.
func ValidSupportGrantRequest(r NewSupportGrantRequest) error {
	if r.OrgID == "" || r.OperatorID == "" || !ValidSupportReason(r.Reason) || len(r.AppIDs) > SupportGrantMaxApps ||
		r.TTL < 0 || r.TTL > SupportGrantMaxTTL || (r.TTL != 0 && r.TTL < time.Hour) {
		return ErrInvalidArgument
	}
	for _, id := range append(slices.Clone(r.AppIDs), nonEmpty(r.ExtendsGrantID)...) {
		if _, err := uuid.Parse(id); err != nil {
			return ErrInvalidArgument
		}
	}
	return nil
}

func nonEmpty(s string) []string {
	if s == "" {
		return nil
	}
	return []string{s}
}

// supportGrantSummaryWindow bounds how long after its end a grant still
// gets a summary, so a loop that was down for days does not mail old news.
const supportGrantSummaryWindow = 24 * time.Hour

// ValidSupportGrant checks a grant before it is stored.
func ValidSupportGrant(g NewSupportGrant) error {
	if g.OrgID == "" || g.CreatedBy == "" || !ValidSupportReason(g.Reason) ||
		g.TTL <= 0 || g.TTL > SupportGrantMaxTTL || len(g.AppIDs) > SupportGrantMaxApps {
		return ErrInvalidArgument
	}
	for _, id := range g.AppIDs {
		if _, err := uuid.Parse(id); err != nil {
			return ErrInvalidArgument
		}
	}
	return nil
}

// ---- MemStore ----

type memSupportGrants struct {
	grants     map[string]SupportGrant
	requests   map[string]SupportGrantRequest
	summarized map[string]bool
}

func (m *MemStore) supportGrantsLocked() *memSupportGrants {
	if m.supportGrants.grants == nil {
		m.supportGrants.grants = map[string]SupportGrant{}
		m.supportGrants.requests = map[string]SupportGrantRequest{}
		m.supportGrants.summarized = map[string]bool{}
	}
	return &m.supportGrants
}

func (m *MemStore) CreateSupportGrant(_ context.Context, g NewSupportGrant) (SupportGrant, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.createSupportGrantLocked(g)
}

func (m *MemStore) createSupportGrantLocked(g NewSupportGrant) (SupportGrant, error) {
	if err := ValidSupportGrant(g); err != nil {
		return SupportGrant{}, err
	}
	org, ok := m.orgs[g.OrgID]
	if !ok {
		return SupportGrant{}, ErrNotFound
	}
	now := time.Now().UTC()
	grant := SupportGrant{
		ID: uuid.NewString(), OrgID: g.OrgID, OrgSlug: org.Slug, AppIDs: slices.Clone(g.AppIDs),
		CreatedBy: g.CreatedBy, RequestID: g.RequestID, Reason: g.Reason, CreatedAt: now, ExpiresAt: now.Add(g.TTL),
	}
	m.supportGrantsLocked().grants[grant.ID] = grant
	return grant, nil
}

func (m *MemStore) SupportGrantByID(_ context.Context, id string) (SupportGrant, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	grant, ok := m.supportGrantsLocked().grants[id]
	if !ok {
		return SupportGrant{}, ErrNotFound
	}
	return grant, nil
}

func (m *MemStore) UsableSupportGrant(_ context.Context, id string) (SupportGrant, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	grant, ok := m.supportGrantsLocked().grants[id]
	if !ok || !m.supportGrantUsableLocked(grant) {
		return SupportGrant{}, ErrNotFound
	}
	return grant, nil
}

// supportGrantUsableLocked mirrors the UsableGrant/LockSupportGrant queries.
func (m *MemStore) supportGrantUsableLocked(grant SupportGrant) bool {
	if !grant.Live(time.Now()) {
		return false
	}
	acct, ok := m.accounts[grant.CreatedBy]
	if !ok || acct.Status != AccountActive && acct.Status != AccountPastDue {
		return false
	}
	org := m.orgs[grant.OrgID]
	if org.Personal && org.PersonalOwnerAccountID != nil && *org.PersonalOwnerAccountID == grant.CreatedBy {
		return true
	}
	mem, ok := m.memberships[orgAccountKey{OrgID: grant.OrgID, AccountID: grant.CreatedBy}]
	return ok && mem.RemovedAt == nil && (mem.Role == OrgRoleOwner || mem.Role == OrgRoleAdmin)
}

// ListSupportGrants lists every live grant of the organization, then its
// most recent ended ones, as the ListGrants query does.
func (m *MemStore) ListSupportGrants(_ context.Context, orgID string) ([]SupportGrant, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	out := m.listSupportGrantsLocked(func(g SupportGrant) bool { return g.OrgID == orgID && g.Live(now) }, false)
	ended := m.listSupportGrantsLocked(func(g SupportGrant) bool { return g.OrgID == orgID && !g.Live(now) }, false)
	if len(ended) > api.SupportGrantHistoryLimit {
		ended = ended[:api.SupportGrantHistoryLimit]
	}
	return append(out, ended...), nil
}

func (m *MemStore) ListLiveSupportGrants(context.Context) ([]SupportGrant, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	return m.listSupportGrantsLocked(func(g SupportGrant) bool { return g.Live(now) }, true), nil
}

func (m *MemStore) listSupportGrantsLocked(keep func(SupportGrant) bool, byExpiry bool) []SupportGrant {
	var out []SupportGrant
	for _, g := range m.supportGrantsLocked().grants {
		if keep(g) {
			out = append(out, g)
		}
	}
	slices.SortFunc(out, func(a, b SupportGrant) int {
		if byExpiry {
			return a.ExpiresAt.Compare(b.ExpiresAt)
		}
		return b.CreatedAt.Compare(a.CreatedAt)
	})
	return out
}

func (m *MemStore) RevokeSupportGrant(_ context.Context, orgID, id, revokedBy string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	grants := m.supportGrantsLocked().grants
	grant, ok := grants[id]
	if !ok || grant.OrgID != orgID || !grant.Live(time.Now()) {
		return ErrNotFound
	}
	now := time.Now().UTC()
	grant.RevokedAt, grant.RevokedBy = &now, revokedBy
	grants[id] = grant
	return nil
}

func (m *MemStore) CreateSupportGrantRequest(_ context.Context, in NewSupportGrantRequest) (SupportGrantRequest, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ValidSupportGrantRequest(in); err != nil {
		return SupportGrantRequest{}, err
	}
	orgID := in.OrgID
	if _, ok := m.orgs[orgID]; !ok {
		return SupportGrantRequest{}, ErrNotFound
	}
	now := time.Now().UTC()
	open := 0
	for _, r := range m.supportGrantsLocked().requests {
		if r.OrgID == orgID && r.Status == SupportRequestOpen && r.ExpiresAt.After(now) {
			open++
		}
	}
	if open >= SupportGrantMaxOpenRequests {
		return SupportGrantRequest{}, ErrSupportRequestLimit
	}
	req := SupportGrantRequest{ID: uuid.NewString(), OrgID: orgID, OperatorID: in.OperatorID, Reason: in.Reason,
		Status: SupportRequestOpen, CreatedAt: now, ExpiresAt: now.Add(SupportGrantRequestTTL),
		AppIDs: slices.Clone(in.AppIDs), TTL: in.TTL, ExtendsGrantID: in.ExtendsGrantID}
	m.supportGrants.requests[req.ID] = req
	return req, nil
}

func (m *MemStore) ListSupportGrantRequests(_ context.Context, orgID string) ([]SupportGrantRequest, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []SupportGrantRequest
	for _, r := range m.supportGrantsLocked().requests {
		if r.OrgID == orgID {
			out = append(out, r)
		}
	}
	slices.SortFunc(out, func(a, b SupportGrantRequest) int { return b.CreatedAt.Compare(a.CreatedAt) })
	return out, nil
}

func (m *MemStore) decideSupportRequestLocked(orgID, requestID, decidedBy, status string) error {
	requests := m.supportGrantsLocked().requests
	req, ok := requests[requestID]
	if !ok || req.OrgID != orgID || req.Status != SupportRequestOpen || !req.ExpiresAt.After(time.Now()) {
		return ErrNotFound
	}
	now := time.Now().UTC()
	req.Status, req.DecidedAt, req.DecidedBy = status, &now, decidedBy
	requests[requestID] = req
	return nil
}

func (m *MemStore) ApproveSupportGrantRequest(_ context.Context, requestID string, g NewSupportGrant) (SupportGrant, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ValidSupportGrant(g); err != nil {
		return SupportGrant{}, err
	}
	if err := m.decideSupportRequestLocked(g.OrgID, requestID, g.CreatedBy, SupportRequestApproved); err != nil {
		return SupportGrant{}, err
	}
	g.RequestID = requestID
	return m.createSupportGrantLocked(g)
}

func (m *MemStore) DeclineSupportGrantRequest(_ context.Context, orgID, requestID, decidedBy string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.decideSupportRequestLocked(orgID, requestID, decidedBy, SupportRequestDeclined)
}

func (m *MemStore) ClaimEndedSupportGrants(_ context.Context, limit int) ([]SupportGrant, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	sg := m.supportGrantsLocked()
	now := time.Now()
	var out []SupportGrant
	for id, g := range sg.grants {
		if sg.summarized[id] || g.Live(now) || !supportGrantEnd(g).After(now.Add(-supportGrantSummaryWindow)) {
			continue
		}
		out = append(out, g)
	}
	slices.SortFunc(out, func(a, b SupportGrant) int { return supportGrantEnd(a).Compare(supportGrantEnd(b)) })
	if len(out) > limit {
		out = out[:limit]
	}
	for _, g := range out {
		sg.summarized[g.ID] = true
	}
	return out, nil
}

// supportGrantEnd is when a grant that is no longer live ended.
func supportGrantEnd(g SupportGrant) time.Time {
	if g.RevokedAt != nil && g.RevokedAt.Before(g.ExpiresAt) {
		return *g.RevokedAt
	}
	return g.ExpiresAt
}

// checkSupportGrantFenceLocked is the memory twin of LockSupportGrant.
func (m *MemStore) checkSupportGrantFenceLocked(ctx context.Context, f OrgAccessFence) error {
	grant, ok := m.supportGrantsLocked().grants[f.SupportGrantID]
	if !ok || grant.OrgID != f.OrgID || !m.supportGrantUsableLocked(grant) {
		return rejectOrgAccess(ctx)
	}
	return nil
}

// ---- PgStore ----

func supportGrantFromRow(row grantsql.GrantByIDRow) SupportGrant {
	grant := SupportGrant{
		ID: pgUUIDString(row.ID), OrgID: pgUUIDString(row.OrgID), OrgSlug: row.OrgSlug,
		AppIDs: pgUUIDStrings(row.AppIds), CreatedBy: pgUUIDString(row.CreatedByAccountID),
		RequestID: pgUUIDStringNullable(row.RequestID), Reason: row.Reason,
		CreatedAt: row.CreatedAt.Time, ExpiresAt: row.ExpiresAt.Time, RevokedAt: timestamptzPtr(row.RevokedAt),
		RevokedBy: pgUUIDStringNullable(row.RevokedByAccountID),
	}
	return grant
}

func supportRequestFromRow(row grantsql.SupportGrantRequest) SupportGrantRequest {
	req := SupportGrantRequest{
		ID: pgUUIDString(row.ID), OrgID: pgUUIDString(row.OrgID), OperatorID: pgUUIDString(row.OperatorAccountID),
		Reason: row.Reason, Status: row.Status, CreatedAt: row.CreatedAt.Time, ExpiresAt: row.ExpiresAt.Time,
		DecidedAt: timestamptzPtr(row.DecidedAt), DecidedBy: pgUUIDStringNullable(row.DecidedByAccountID),
		AppIDs: pgUUIDStrings(row.AppIds), ExtendsGrantID: pgUUIDStringNullable(row.ExtendsGrantID),
	}
	if row.TtlSeconds.Valid {
		req.TTL = time.Duration(row.TtlSeconds.Int32) * time.Second
	}
	return req
}

func insertSupportGrantParams(g NewSupportGrant) (grantsql.InsertGrantParams, error) {
	if err := ValidSupportGrant(g); err != nil {
		return grantsql.InsertGrantParams{}, err
	}
	org, err := parsePgUUID(g.OrgID)
	if err != nil {
		return grantsql.InsertGrantParams{}, ErrInvalidArgument
	}
	by, err := parsePgUUID(g.CreatedBy)
	if err != nil {
		return grantsql.InsertGrantParams{}, ErrInvalidArgument
	}
	params := grantsql.InsertGrantParams{OrgID: org, CreatedBy: by, Reason: g.Reason, TtlSeconds: g.TTL.Seconds()}
	for _, id := range g.AppIDs {
		params.AppIds = append(params.AppIds, mustPgUUID(id))
	}
	if g.RequestID != "" {
		params.RequestID = mustPgUUID(g.RequestID)
	}
	return params, nil
}

func (s *PgStore) CreateSupportGrant(ctx context.Context, g NewSupportGrant) (SupportGrant, error) {
	params, err := insertSupportGrantParams(g)
	if err != nil {
		return SupportGrant{}, err
	}
	row, err := grantsql.New().InsertGrant(ctx, s.pool, params)
	if err != nil {
		return SupportGrant{}, fmt.Errorf("state: create support grant: %w", mapErr(err))
	}
	return s.SupportGrantByID(ctx, pgUUIDString(row.ID))
}

func (s *PgStore) SupportGrantByID(ctx context.Context, id string) (SupportGrant, error) {
	key, err := parsePgUUID(id)
	if err != nil {
		return SupportGrant{}, ErrNotFound
	}
	row, err := grantsql.New().GrantByID(ctx, s.pool, key)
	if errors.Is(err, pgx.ErrNoRows) {
		return SupportGrant{}, ErrNotFound
	}
	if err != nil {
		return SupportGrant{}, fmt.Errorf("state: read support grant: %w", mapErr(err))
	}
	return supportGrantFromRow(row), nil
}

func (s *PgStore) UsableSupportGrant(ctx context.Context, id string) (SupportGrant, error) {
	key, err := parsePgUUID(id)
	if err != nil {
		return SupportGrant{}, ErrNotFound
	}
	if _, err := grantsql.New().UsableGrant(ctx, s.pool, key); errors.Is(err, pgx.ErrNoRows) {
		return SupportGrant{}, ErrNotFound
	} else if err != nil {
		return SupportGrant{}, fmt.Errorf("state: check support grant: %w", mapErr(err))
	}
	return s.SupportGrantByID(ctx, id)
}

func (s *PgStore) ListSupportGrants(ctx context.Context, orgID string) ([]SupportGrant, error) {
	org, err := parsePgUUID(orgID)
	if err != nil {
		return nil, ErrNotFound
	}
	rows, err := grantsql.New().ListGrants(ctx, s.pool, grantsql.ListGrantsParams{OrgID: org, HistoryLimit: api.SupportGrantHistoryLimit})
	if err != nil {
		return nil, fmt.Errorf("state: list support grants: %w", mapErr(err))
	}
	out := make([]SupportGrant, 0, len(rows))
	for _, row := range rows {
		out = append(out, supportGrantFromRow(grantsql.GrantByIDRow(row)))
	}
	return out, nil
}

func (s *PgStore) ListLiveSupportGrants(ctx context.Context) ([]SupportGrant, error) {
	rows, err := grantsql.New().ListLiveGrants(ctx, s.pool)
	if err != nil {
		return nil, fmt.Errorf("state: list live support grants: %w", mapErr(err))
	}
	out := make([]SupportGrant, 0, len(rows))
	for _, row := range rows {
		out = append(out, supportGrantFromRow(grantsql.GrantByIDRow(row)))
	}
	return out, nil
}

func (s *PgStore) RevokeSupportGrant(ctx context.Context, orgID, id, revokedBy string) error {
	org, err1 := parsePgUUID(orgID)
	key, err2 := parsePgUUID(id)
	by, err3 := parsePgUUID(revokedBy)
	if err1 != nil || err2 != nil || err3 != nil {
		return ErrNotFound
	}
	n, err := grantsql.New().RevokeGrant(ctx, s.pool, grantsql.RevokeGrantParams{ID: key, OrgID: org, RevokedBy: by})
	if err != nil {
		return fmt.Errorf("state: revoke support grant: %w", mapErr(err))
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PgStore) CreateSupportGrantRequest(ctx context.Context, in NewSupportGrantRequest) (SupportGrantRequest, error) {
	if err := ValidSupportGrantRequest(in); err != nil {
		return SupportGrantRequest{}, err
	}
	org, err1 := parsePgUUID(in.OrgID)
	operator, err2 := parsePgUUID(in.OperatorID)
	if err1 != nil || err2 != nil {
		return SupportGrantRequest{}, ErrInvalidArgument
	}
	params := grantsql.InsertRequestParams{
		OrgID: org, OperatorID: operator, Reason: in.Reason,
		TtlSeconds: SupportGrantRequestTTL.Seconds(), MaxOpen: SupportGrantMaxOpenRequests,
	}
	for _, id := range in.AppIDs {
		params.AppIds = append(params.AppIds, mustPgUUID(id))
	}
	if in.TTL > 0 {
		params.GrantTtlSeconds = pgtype.Int4{Int32: int32(in.TTL / time.Second), Valid: true}
	}
	if in.ExtendsGrantID != "" {
		params.ExtendsGrantID = mustPgUUID(in.ExtendsGrantID)
	}
	var row grantsql.SupportGrantRequest
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := grantsql.New()
		if err := q.LockOrgRequests(ctx, tx, org); err != nil {
			return err
		}
		var err error
		row, err = q.InsertRequest(ctx, tx, params)
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return SupportGrantRequest{}, ErrSupportRequestLimit
	}
	if err != nil {
		return SupportGrantRequest{}, fmt.Errorf("state: create support access request: %w", mapErr(err))
	}
	return supportRequestFromRow(row), nil
}

func (s *PgStore) ListSupportGrantRequests(ctx context.Context, orgID string) ([]SupportGrantRequest, error) {
	org, err := parsePgUUID(orgID)
	if err != nil {
		return nil, ErrNotFound
	}
	rows, err := grantsql.New().ListRequests(ctx, s.pool, org)
	if err != nil {
		return nil, fmt.Errorf("state: list support access requests: %w", mapErr(err))
	}
	out := make([]SupportGrantRequest, 0, len(rows))
	for _, row := range rows {
		out = append(out, supportRequestFromRow(row))
	}
	return out, nil
}

func decideSupportRequestParams(orgID, requestID, decidedBy, status string) (grantsql.DecideRequestParams, error) {
	org, err1 := parsePgUUID(orgID)
	key, err2 := parsePgUUID(requestID)
	by, err3 := parsePgUUID(decidedBy)
	if err1 != nil || err2 != nil || err3 != nil {
		return grantsql.DecideRequestParams{}, ErrNotFound
	}
	return grantsql.DecideRequestParams{ID: key, OrgID: org, DecidedBy: by, Status: status}, nil
}

func (s *PgStore) ApproveSupportGrantRequest(ctx context.Context, requestID string, g NewSupportGrant) (SupportGrant, error) {
	g.RequestID = requestID
	params, err := insertSupportGrantParams(g)
	if err != nil {
		return SupportGrant{}, err
	}
	decide, err := decideSupportRequestParams(g.OrgID, requestID, g.CreatedBy, SupportRequestApproved)
	if err != nil {
		return SupportGrant{}, err
	}
	var id pgtype.UUID
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := grantsql.New()
		if _, err := q.DecideRequest(ctx, tx, decide); errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		} else if err != nil {
			return err
		}
		row, err := q.InsertGrant(ctx, tx, params)
		id = row.ID
		return err
	})
	if errors.Is(err, ErrNotFound) {
		return SupportGrant{}, ErrNotFound
	}
	if err != nil {
		return SupportGrant{}, fmt.Errorf("state: approve support access request: %w", mapErr(err))
	}
	return s.SupportGrantByID(ctx, pgUUIDString(id))
}

func (s *PgStore) DeclineSupportGrantRequest(ctx context.Context, orgID, requestID, decidedBy string) error {
	decide, err := decideSupportRequestParams(orgID, requestID, decidedBy, SupportRequestDeclined)
	if err != nil {
		return err
	}
	if _, err := grantsql.New().DecideRequest(ctx, s.pool, decide); errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return fmt.Errorf("state: decline support access request: %w", mapErr(err))
	}
	return nil
}

func (s *PgStore) ClaimEndedSupportGrants(ctx context.Context, limit int) ([]SupportGrant, error) {
	if limit <= 0 {
		return nil, nil
	}
	rows, err := grantsql.New().ClaimEndedGrants(ctx, s.pool, int32(min(limit, 1000)))
	if err != nil {
		return nil, fmt.Errorf("state: claim ended support grants: %w", mapErr(err))
	}
	out := make([]SupportGrant, 0, len(rows))
	for _, row := range rows {
		out = append(out, supportGrantFromRow(grantsql.GrantByIDRow(row)))
	}
	return out, nil
}
