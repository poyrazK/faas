// adr: 531
package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// ServicePolicyReader exposes only the customer policy reads used by managed
// service discovery and authorization. A snapshot reader cannot mutate intent.
type ServicePolicyReader interface {
	AppByID(context.Context, string) (App, error)
	AppBySlug(context.Context, string) (App, error)
	PreviewAppByProjectWorkload(context.Context, string, string, int, string) (App, error)
	ScenarioTestMemberByApp(context.Context, string) (ScenarioTestMember, error)
	ScenarioTestAppByWorkload(context.Context, string, string, string) (App, error)
	GetGitHubDeployPolicy(context.Context, string, string) (GitHubDeployPolicy, error)
}

type ServicePolicySnapshotStore interface {
	WithServicePolicySnapshot(context.Context, func(ServicePolicyReader) error) error
}

// WithServicePolicySnapshot ends the read-only transaction before wake or
// forwarding. All reads observe one committed view, even without notifications.
func (s *PgStore) WithServicePolicySnapshot(ctx context.Context, read func(ServicePolicyReader) error) error {
	if s == nil || s.pool == nil || read == nil {
		return errors.New("service policy snapshot is unavailable")
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return fmt.Errorf("begin service policy snapshot: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := read(servicePolicyReader{tx: tx}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

type servicePolicyReader struct{ tx pgx.Tx }

func decodeServicePolicyApp(row sqlc.ReadServicePolicyAppByIDRow, err error) (App, error) {
	if err != nil {
		return App{}, mapErr(err)
	}
	app := App{ID: pgUUIDString(row.ID), AccountID: pgUUIDString(row.AccountID), Slug: row.Slug,
		Status: AppStatus(row.Status), ProjectID: pgUUIDString(row.ProjectID),
		PreviewOfSlug: row.PreviewOfSlug.String, PreviewPrNumber: int(row.PreviewPrNumber.Int32),
		PreviewPrState: row.PreviewPrState.String, AppProtocol: row.AppProtocol, WebSocketEnabled: row.WebsocketEnabled}
	if row.PreviewExpiresAt.Valid {
		at := row.PreviewExpiresAt.Time
		app.PreviewExpiresAt = &at
	}
	if len(row.Manifest) != 0 {
		if err := json.Unmarshal(row.Manifest, &app.Manifest); err != nil {
			return App{}, fmt.Errorf("decode service policy: %w", err)
		}
	}
	return app, nil
}

func (s servicePolicyReader) AppByID(ctx context.Context, id string) (App, error) {
	row, err := sqlc.New().ReadServicePolicyAppByID(ctx, s.tx, uuidToPgtype(id))
	return decodeServicePolicyApp(row, err)
}

func (s servicePolicyReader) AppBySlug(ctx context.Context, slug string) (App, error) {
	row, err := sqlc.New().ReadServicePolicyAppBySlug(ctx, s.tx, slug)
	return decodeServicePolicyApp(sqlc.ReadServicePolicyAppByIDRow(row), err)
}

func (s servicePolicyReader) PreviewAppByProjectWorkload(ctx context.Context, account, project string, pr int, workload string) (App, error) {
	row, err := sqlc.New().ReadServicePolicyPreviewApp(ctx, s.tx, sqlc.ReadServicePolicyPreviewAppParams{
		AccountID: uuidToPgtype(account), ProjectID: uuidToPgtype(project), PreviewPrNumber: int32(pr), WorkloadName: workload})
	return decodeServicePolicyApp(sqlc.ReadServicePolicyAppByIDRow(row), err)
}

func (s servicePolicyReader) ScenarioTestAppByWorkload(ctx context.Context, account, run, workload string) (App, error) {
	row, err := sqlc.New().ReadServicePolicyTestApp(ctx, s.tx, sqlc.ReadServicePolicyTestAppParams{
		AccountID: uuidToPgtype(account), RunID: run, WorkloadName: workload})
	return decodeServicePolicyApp(sqlc.ReadServicePolicyAppByIDRow(row), err)
}

func (s servicePolicyReader) ScenarioTestMemberByApp(ctx context.Context, appID string) (ScenarioTestMember, error) {
	row, err := sqlc.New().ReadServicePolicyTestMember(ctx, s.tx, uuidToPgtype(appID))
	if err != nil {
		return ScenarioTestMember{}, mapErr(err)
	}
	return ScenarioTestMember{AccountID: pgUUIDString(row.AccountID), RunID: row.RunID, Workload: row.WorkloadName, AppID: pgUUIDString(row.AppID)}, nil
}

func (s servicePolicyReader) GetGitHubDeployPolicy(ctx context.Context, project, account string) (GitHubDeployPolicy, error) {
	row, err := sqlc.New().ReadServicePolicyProject(ctx, s.tx, sqlc.ReadServicePolicyProjectParams{
		ProjectID: uuidToPgtype(project), AccountID: uuidToPgtype(account)})
	if errors.Is(err, pgx.ErrNoRows) {
		return DefaultGitHubDeployPolicy(project, account), nil
	}
	if err != nil {
		return GitHubDeployPolicy{}, err
	}
	policy := DefaultGitHubDeployPolicy(project, account)
	policy.PreviewServicePolicy = PreviewServicePolicy(row)
	return policy, policy.Validate()
}
