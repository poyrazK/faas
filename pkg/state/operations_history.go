package state

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/operations"
)

type operationHistoryCursor struct {
	Version   int       `json:"v"`
	Query     string    `json:"query"`
	CreatedAt time.Time `json:"created_at"`
	ID        string    `json:"id"`
}

var operationHistoryName = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

func prepareOperationHistory(account, tenant string, opts api.OperationListOptions) (api.OperationListOptions, operationHistoryCursor, error) {
	return prepareOperationHistoryQuery(account, tenant, opts, false)
}

func prepareOperationHistoryQuery(account, tenant string, opts api.OperationListOptions, operator bool) (api.OperationListOptions, operationHistoryCursor, error) {
	var cursor operationHistoryCursor
	accountID, a := uuid.Parse(account)
	tenantID, t := uuid.Parse(tenant)
	appID, p := uuid.Parse(opts.AppID)
	if a != nil || (!operator || tenant != "") && (t != nil || tenantID == uuid.Nil) || p != nil || accountID == uuid.Nil || appID == uuid.Nil || api.ValidateScope(opts.Scope) != nil {
		return opts, cursor, ErrInvalidArgument
	}
	opts.AppID = appID.String()
	if operator && tenant != "" {
		opts.TenantID = tenantID.String()
	}
	if opts.Limit == 0 {
		opts.Limit = api.OperationHistoryPageDefault
	}
	if opts.Limit < 1 || opts.Limit > api.OperationHistoryPageMax || len(opts.Cursor) > api.OperationHistoryCursorMaxBytes || (opts.Name != "" && (len(opts.Name) > api.OperationNameMaxBytes || !operationHistoryName.MatchString(opts.Name))) {
		return opts, cursor, ErrInvalidArgument
	}
	switch opts.State {
	case "", api.OperationAccepted, api.OperationRunning, api.OperationSucceeded, api.OperationFailed, api.OperationCancelled, api.OperationRequiresReconciliation:
	default:
		return opts, cursor, ErrInvalidArgument
	}
	selectors := []string{accountID.String(), tenantID.String(), opts.AppID, opts.Scope, opts.Name, string(opts.State)}
	if operator {
		selectors = append(selectors, "account-operator")
	}
	query, _ := json.Marshal(selectors)
	digest := fmt.Sprintf("%x", sha256.Sum256(query))
	if opts.Cursor == "" {
		return opts, operationHistoryCursor{Version: 1, Query: digest}, nil
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(opts.Cursor)
	if err != nil {
		return opts, cursor, ErrInvalidArgument
	}
	raw, err = operations.CanonicalJSON(raw)
	if err != nil {
		return opts, cursor, ErrInvalidArgument
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cursor); err != nil {
		return opts, cursor, ErrInvalidArgument
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return opts, cursor, ErrInvalidArgument
	}
	id, err := uuid.Parse(cursor.ID)
	if err != nil || id == uuid.Nil || id.String() != cursor.ID || cursor.Version != 1 || cursor.Query != digest || cursor.CreatedAt.IsZero() || !cursor.CreatedAt.Equal(cursor.CreatedAt.Truncate(time.Microsecond)) {
		return opts, cursor, ErrInvalidArgument
	}
	return opts, cursor, nil
}

func operationHistoryPage(rows []api.OperationSummary, limit int, cursor operationHistoryCursor) api.OperationListResponse {
	page := api.OperationListResponse{Operations: rows}
	if len(rows) > limit {
		page.Operations = rows[:limit]
		last := rows[limit-1]
		cursor.CreatedAt, cursor.ID = last.CreatedAt.UTC().Truncate(time.Microsecond), last.ID
		raw, _ := json.Marshal(cursor)
		page.NextCursor = base64.RawURLEncoding.EncodeToString(raw)
	}
	return page
}

func operationHistorySummary(op Operation) api.OperationSummary {
	summary := api.OperationSummary{ID: op.ID, Name: op.Name, Generation: op.Generation, State: op.State,
		CancellationRequested: op.CancellationRequested, LatestSequence: op.LatestSequence,
		CreatedAt: op.CreatedAt.UTC().Truncate(time.Microsecond), UpdatedAt: op.UpdatedAt, ExpiresAt: op.ExpiresAt,
		CompletionDelivery: api.OperationDeliverySummary{State: op.CompletionDelivery.State, Attempts: op.CompletionDelivery.Attempts}}
	if op.Progress != nil {
		progress := *op.Progress
		summary.Progress = &progress
	}
	if op.CompletionDelivery.NextAttemptAt != nil {
		next := *op.CompletionDelivery.NextAttemptAt
		summary.CompletionDelivery.NextAttemptAt = &next
	}
	return summary
}

func sameOperationHistoryIdentity(left, right string) bool {
	a, err := uuid.Parse(left)
	if err != nil {
		return false
	}
	b, err := uuid.Parse(right)
	return err == nil && a == b
}
