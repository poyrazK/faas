package state_test

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func TestPgIssueCustomerImpactDeduplicatesLinkedIdentities(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	account, app := seedPgAccountAndApp(t, s, ctx)
	deployment := seedPgDeployment(t, s, ctx, app)
	now := time.Now().UTC().Truncate(time.Microsecond)
	consumer, other, tenant := uuid.NewString(), uuid.NewString(), uuid.NewString()
	if _, err := pool.Exec(ctx, `INSERT INTO platform_tenants(id,account_id,external_ref,name) VALUES($1,$2,$3,$3)`, tenant, account.ID, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	var id string
	for _, identity := range []struct{ consumer, tenant string }{{consumer, ""}, {consumer, tenant}, {other, ""}, {"", ""}} {
		event := api.IssueEvent{EventID: uuid.NewString(), OccurredAt: now, ExceptionType: "Error", SourceKind: "exception"}
		out, err := s.RecordIssue(ctx, state.RecordIssueParams{Credential: state.IssueCredential{AccountID: account.ID, AppID: app.ID, DeploymentID: deployment.ID, Environment: "application"}, Event: event, Fingerprint: strings.Repeat("a", 64), Title: "Error", PayloadHash: strings.Repeat("b", 64), GroupingVersion: 1, Limits: api.PlanPro.IssueLimits(), Now: now})
		if err != nil {
			t.Fatal(err)
		}
		id = out.IssueID
		if _, err = pool.Exec(ctx, `UPDATE issue_events SET verified_consumer_id=NULLIF($1,'')::uuid,verified_platform_tenant_id=NULLIF($2,'')::uuid WHERE app_id=$3 AND event_id=$4`, identity.consumer, identity.tenant, app.ID, event.EventID); err != nil {
			t.Fatal(err)
		}
	}
	q := sqlc.New()
	issueID, appID, accountID, depID := state.NewPgtypeUUID(uuid.MustParse(id)), state.NewPgtypeUUID(uuid.MustParse(app.ID)), state.NewPgtypeUUID(uuid.MustParse(account.ID)), state.NewPgtypeUUID(uuid.MustParse(deployment.ID))
	since, until := state.NewPgtypeTime(now.Add(-time.Minute)), state.NewPgtypeTime(now.Add(time.Minute))
	impact, err := q.IssueImpact(ctx, pool, sqlc.IssueImpactParams{IssueID: issueID, Since: since, Until: until})
	if err != nil || impact.IdentifiedCustomers != 2 || impact.ObservedEvents != 4 || impact.UnattributedEvents != 1 {
		t.Fatalf("impact: %+v, %v", impact, err)
	}
	count, err := q.IssueImpactCustomerCount(ctx, pool, sqlc.IssueImpactCustomerCountParams{IssueID: issueID, Since: since, Until: until})
	if err != nil || count != 2 {
		t.Fatalf("impact count: %d, %v", count, err)
	}
	summaries, err := q.IssueImpactSummaries(ctx, pool, sqlc.IssueImpactSummariesParams{IssueIds: []pgtype.UUID{issueID}, Since: since, Until: until})
	if err != nil || len(summaries) != 1 || summaries[0].IdentifiedCustomers != 2 {
		t.Fatalf("summaries: %+v, %v", summaries, err)
	}
	list, err := q.IssueListByImpact(ctx, pool, sqlc.IssueListByImpactParams{AppID: appID, TagFilter: []byte(`{}`), Since: since, Until: until, PageLimit: 10})
	if err != nil || len(list) != 1 || list[0].IdentifiedCustomers != 2 {
		t.Fatalf("list: %+v, %v", list, err)
	}
	compound, err := q.IssueCompoundCount(ctx, pool, sqlc.IssueCompoundCountParams{IssueID: issueID, Since: since, Until: until})
	if err != nil || compound.IdentifiedCustomers != 2 || compound.ObservedEvents != 4 {
		t.Fatalf("compound: %+v, %v", compound, err)
	}
	health, err := q.IssueReleaseHealth(ctx, pool, sqlc.IssueReleaseHealthParams{AppID: appID, AccountID: accountID, DeploymentID: depID, Environment: "application", WindowStart: since, WindowEnd: until})
	if err != nil || health.IdentifiedCustomers != 2 || health.ObservedIssueEvents != 4 {
		t.Fatalf("release health: %+v, %v", health, err)
	}
}
