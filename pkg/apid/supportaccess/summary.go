package supportaccess

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/mail"
	"github.com/onebox-faas/faas/pkg/state"
)

// SummaryMailKind is the end-of-access summary mail.
const SummaryMailKind = "support_access_summary"

// summaryBatch bounds the grants one pass claims.
const summaryBatch = 20

// summaryActivityScan bounds the activity rows read for one summary; the
// mail lists at most summaryListed of the grant's actions.
const (
	summaryActivityScan = 500
	summaryListed       = 30
)

// SummarizeEnded mails the end-of-access summary of every grant that ended
// since the last pass, and reports how many it sent. Each grant is claimed
// once across every apid, so the mail is sent at most once; a failed send
// is logged by the mailer and not retried.
func (s *Service) SummarizeEnded(ctx context.Context) (int, error) {
	grants, err := s.d.Store.ClaimEndedSupportGrants(ctx, summaryBatch)
	if err != nil {
		return 0, err
	}
	for _, g := range grants {
		s.summarize(ctx, g)
	}
	return len(grants), nil
}

func (s *Service) summarize(ctx context.Context, g state.SupportGrant) {
	if s.d.Mail == nil {
		return
	}
	names := s.newNames()
	view := names.view(ctx, g)
	summary := mail.SupportAccessSummary{
		OrgName: g.OrgSlug, OrgSlug: g.OrgSlug, Apps: "all apps", GrantedBy: view.CreatedBy, Started: g.CreatedAt,
		Ended: g.ExpiresAt, PageURL: s.dashboardURL(PagePath),
	}
	if org, err := s.d.Store.OrgByID(ctx, g.OrgID); err == nil && org.Name != "" {
		summary.OrgName = org.Name
	}
	if !view.AllApps {
		summary.Apps = strings.Join(view.Apps, ", ")
	}
	if g.RevokedAt != nil && g.RevokedAt.Before(g.ExpiresAt) {
		summary.Ended, summary.EndedBy = *g.RevokedAt, view.RevokedBy
		if summary.EndedBy == "" {
			summary.EndedBy = "an owner or admin"
		}
	}
	if summary.GrantedBy == "" {
		summary.GrantedBy = "An owner or admin"
	}
	summary.Actions, summary.More = s.grantActions(ctx, g, summary.Ended)
	subject, body := mail.SupportAccessSummaryBody(summary)
	s.d.Mail(ctx, SummaryMailKind, g.OrgID, subject, body)
	s.audit(ctx, "support_access.summary_sent", g.CreatedBy, map[string]any{"org_id": g.OrgID, "grant_id": g.ID, "actions": len(summary.Actions) + summary.More})
}

// grantActions reads the changes support made under g from the
// organization's activity log, oldest first.
func (s *Service) grantActions(ctx context.Context, g state.SupportGrant, ended time.Time) ([]mail.SupportAccessAction, int) {
	org, err := uuid.Parse(g.OrgID)
	if err != nil {
		return nil, 0
	}
	rows, err := s.d.Store.ListOrgActivity(ctx, state.OrgActivityFilter{
		OrgID: org, KindPrefix: "support_access.action", ActorType: state.OrgActivityActorOperator, Limit: summaryActivityScan,
	})
	if err != nil {
		return nil, 0
	}
	var actions []mail.SupportAccessAction
	for _, row := range rows {
		if row.OccurredAt.Before(g.CreatedAt) || row.OccurredAt.After(ended.Add(time.Minute)) {
			continue
		}
		var data struct {
			GrantID string `json:"grant_id"`
			Route   string `json:"route"`
			App     string `json:"app"`
			Status  int    `json:"status"`
		}
		if json.Unmarshal(row.Data, &data) != nil || data.GrantID != g.ID {
			continue
		}
		actions = append(actions, mail.SupportAccessAction{
			At: row.OccurredAt, Operator: row.ActorLabel, App: data.App, What: actionLabel(data.Route), Status: data.Status,
		})
	}
	// The activity log lists newest first; the mail reads oldest first.
	for i, j := 0, len(actions)-1; i < j; i, j = i+1, j-1 {
		actions[i], actions[j] = actions[j], actions[i]
	}
	more := 0
	if len(actions) > summaryListed {
		more = len(actions) - summaryListed
		actions = actions[:summaryListed]
	}
	return actions, more
}

// actionLabels names the support routes that change something.
var actionLabels = map[string]string{
	"POST /v1/apps/{slug}/deployments/source-ref":  "deploy of the production branch",
	"POST /v1/deployments/{id}/retry":              "deployment retry",
	"POST /v1/apps/{slug}/deployments/{id}/cancel": "deployment cancel",
	"POST /v1/apps/{slug}/rollback":                "rollback",
	"POST /v1/apps/{slug}/restart":                 "restart",
}

func actionLabel(route string) string {
	if label, ok := actionLabels[route]; ok {
		return label
	}
	return route
}
