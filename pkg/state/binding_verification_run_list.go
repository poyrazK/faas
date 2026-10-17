package state

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/onebox-faas/faas/pkg/api"
	"slices"
	"time"
)

type VerificationRunListStore interface {
	ListBindingVerificationRuns(context.Context, string, string, api.BindingVerificationRunListOptions) (api.BindingVerificationRunList, error)
	ListProjectBindingVerificationRuns(context.Context, string, string, string, api.BindingVerificationRunListOptions) (api.BindingVerificationRunList, error)
}
type verificationRunListCursor struct {
	Version    int       `json:"v"`
	Selection  string    `json:"selection"`
	ObservedAt time.Time `json:"at"`
	BeforeAt   time.Time `json:"before_at"`
	BeforeID   string    `json:"before_id"`
}

func prepareVerificationRunList(account, owner, environment string, o *api.BindingVerificationRunListOptions) (verificationRunListCursor, error) {
	if o.DeploymentID != "" {
		o.DeploymentID = stageCanonicalID(o.DeploymentID)
	}
	if o.Limit == 0 {
		o.Limit = api.BindingVerificationRunListDefaultLimit
	}
	if o.Limit < 1 || o.Limit > api.BindingVerificationRunListMaxLimit || len(o.Cursor) > api.BindingVerificationRunListCursorMaxBytes || o.DeploymentID != "" && !canonicalStateUUID(o.DeploymentID) || o.Scope != "" && api.ValidateScope(o.Scope) != nil || !slices.Contains([]string{"", "pending", "passed", "cancelled", "expired"}, o.Status) || environment != "" && (o.Scope != "" || o.IncludeMembers) {
		return verificationRunListCursor{}, ErrInvalidArgument
	}
	selection, _ := json.Marshal([]any{account, owner, environment, o.DeploymentID, o.Scope, o.Status, o.IncludeMembers})
	fingerprint := fmt.Sprintf("%x", sha256.Sum256(selection))
	now := time.Now().UTC().Truncate(time.Microsecond)
	c := verificationRunListCursor{Version: 1, Selection: fingerprint, ObservedAt: now, BeforeAt: now, BeforeID: "00000000-0000-0000-0000-000000000000"}
	if o.Cursor == "" {
		return c, nil
	}
	raw, e := base64.RawURLEncoding.DecodeString(o.Cursor)
	if e != nil || json.Unmarshal(raw, &c) != nil || c.Version != 1 || c.Selection != fingerprint || c.ObservedAt.IsZero() || c.ObservedAt.After(now) || c.BeforeAt.IsZero() || c.BeforeAt.After(c.ObservedAt) || !canonicalStateUUID(c.BeforeID) {
		return c, ErrInvalidArgument
	}
	return c, nil
}
func receiptListStatus(status string, expiry, observed time.Time) string {
	if status != "cancelled" && !expiry.After(observed) {
		return "expired"
	}
	return status
}
func verificationRunListBefore(r api.BindingVerificationRunSummary, c verificationRunListCursor, hasCursor bool) bool {
	return !r.CreatedAt.After(c.ObservedAt) && (!hasCursor || r.CreatedAt.Before(c.BeforeAt) || r.CreatedAt.Equal(c.BeforeAt) && r.ID < c.BeforeID)
}
func finishVerificationRunList(rows []api.BindingVerificationRunSummary, c verificationRunListCursor, limit int) api.BindingVerificationRunList {
	slices.SortFunc(rows, func(a, b api.BindingVerificationRunSummary) int {
		if n := b.CreatedAt.Compare(a.CreatedAt); n != 0 {
			return n
		}
		if a.ID > b.ID {
			return -1
		}
		if a.ID < b.ID {
			return 1
		}
		return 0
	})
	out := api.BindingVerificationRunList{ObservedAt: c.ObservedAt, Runs: rows}
	if out.Runs == nil {
		out.Runs = []api.BindingVerificationRunSummary{}
	}
	if len(rows) > limit {
		out.Runs = rows[:limit]
		last := out.Runs[limit-1]
		c.BeforeAt, c.BeforeID = last.CreatedAt, last.ID
		raw, _ := json.Marshal(c)
		out.NextCursor = base64.RawURLEncoding.EncodeToString(raw)
	}
	return out
}
func (m *MemStore) ListBindingVerificationRuns(ctx context.Context, account, app string, o api.BindingVerificationRunListOptions) (api.BindingVerificationRunList, error) {
	c, e := prepareVerificationRunList(account, app, "", &o)
	if e != nil {
		return api.BindingVerificationRunList{}, e
	}
	if e = ctx.Err(); e != nil {
		return api.BindingVerificationRunList{}, e
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	rows := []api.BindingVerificationRunSummary{}
	if m.apps[app].AccountID != account {
		return finishVerificationRunList(rows, c, o.Limit), nil
	}
	for _, record := range m.bindingVerificationRuns {
		r := record.Report
		if r.Policy.AppID != app || !o.IncludeMembers && r.GraphRunID != "" || o.DeploymentID != "" && stageCanonicalID(r.DeploymentID) != o.DeploymentID || o.Scope != "" && r.Scope != o.Scope {
			continue
		}
		status := receiptListStatus(r.Status, r.ExpiresAt, c.ObservedAt)
		if o.Status != "" && o.Status != status {
			continue
		}
		row := api.BindingVerificationRunSummary{ID: stageCanonicalID(r.ID), App: m.apps[app].Slug, DeploymentID: stageCanonicalID(r.DeploymentID), Scope: r.Scope, GraphRunID: stageCanonicalID(r.GraphRunID), Status: status, CreatedAt: r.CreatedAt, ExpiresAt: r.ExpiresAt}
		if verificationRunListBefore(row, c, o.Cursor != "") {
			rows = append(rows, row)
		}
	}
	return finishVerificationRunList(rows, c, o.Limit), nil
}
func (m *MemStore) ListProjectBindingVerificationRuns(ctx context.Context, account, project, environment string, o api.BindingVerificationRunListOptions) (api.BindingVerificationRunList, error) {
	c, e := prepareVerificationRunList(account, project, environment, &o)
	if e != nil {
		return api.BindingVerificationRunList{}, e
	}
	if e = ctx.Err(); e != nil {
		return api.BindingVerificationRunList{}, e
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	rows := []api.BindingVerificationRunSummary{}
	for _, record := range m.graphVerificationRuns {
		r := record.Report
		if record.AccountID != account || record.ProjectID != project || r.Environment != environment {
			continue
		}
		if o.DeploymentID != "" {
			found := false
			for _, id := range r.Manifest.Deployments {
				found = found || stageCanonicalID(id) == o.DeploymentID
			}
			if !found {
				continue
			}
		}
		status := receiptListStatus(r.Status, r.ExpiresAt, c.ObservedAt)
		if o.Status != "" && o.Status != status {
			continue
		}
		row := api.BindingVerificationRunSummary{ID: stageCanonicalID(r.ID), Project: r.Project, Environment: r.Environment, Status: status, CreatedAt: r.CreatedAt, ExpiresAt: r.ExpiresAt}
		if verificationRunListBefore(row, c, o.Cursor != "") {
			rows = append(rows, row)
		}
	}
	return finishVerificationRunList(rows, c, o.Limit), nil
}
