package state

import (
	"context"
	"encoding/json"
	"errors"
	"slices"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/issues"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func sourceMapMetadata(row sqlc.IssueSourceMapArtifact) api.IssueSourceMapsMetadata {
	return api.IssueSourceMapsMetadata{DeploymentID: issueID(row.DeploymentID), ManifestSHA256: row.ManifestSha256, MapCount: int(row.MapCount), Ready: row.CanonicalManifest != nil, CreatedAt: row.CreatedAt.Time, RetiredAt: issueTimePtr(row.RetiredAt)}
}
func (s *PgStore) PublishIssueSourceMaps(ctx context.Context, account, app, dep, actor string, in api.PublishIssueSourceMapsRequest) (api.IssueSourceMapsMetadata, error) {
	set, err := issues.CompileSourceMaps(in)
	if err != nil {
		return api.IssueSourceMapsMetadata{}, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return api.IssueSourceMapsMetadata{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	owner, err := q.IssueLockApp(ctx, tx, issueUUID(app))
	if err != nil {
		return api.IssueSourceMapsMetadata{}, mapErr(err)
	}
	if issueID(owner.AccountID) != account {
		return api.IssueSourceMapsMetadata{}, ErrNotFound
	}
	if _, err = q.IssueSourceMapDeploymentOwner(ctx, tx, sqlc.IssueSourceMapDeploymentOwnerParams{AccountID: issueUUID(account), AppID: issueUUID(app), DeploymentID: issueUUID(dep)}); err != nil {
		return api.IssueSourceMapsMetadata{}, mapErr(err)
	}
	old, err := q.IssueReadSourceMaps(ctx, tx, sqlc.IssueReadSourceMapsParams{AppID: issueUUID(app), DeploymentID: issueUUID(dep)})
	if err == nil {
		if old.ManifestSha256 != set.Digest {
			return api.IssueSourceMapsMetadata{}, ErrIssueSourceMapsConflict
		}
		if old.CanonicalManifest != nil {
			return sourceMapMetadata(old), nil
		}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return api.IssueSourceMapsMetadata{}, err
	}
	n, err := q.IssueCountReadySourceMaps(ctx, tx, issueUUID(app))
	if err != nil {
		return api.IssueSourceMapsMetadata{}, err
	}
	if n >= api.IssueSourceMapDeploymentsMax {
		return api.IssueSourceMapsMetadata{}, &IssueLimitError{Resource: "source_map_deployments", Limit: api.IssueSourceMapDeploymentsMax, Observed: n + 1}
	}
	raw, err := json.Marshal(set.Request)
	if err != nil {
		return api.IssueSourceMapsMetadata{}, err
	}
	row, err := q.IssuePublishSourceMaps(ctx, tx, sqlc.IssuePublishSourceMapsParams{AppID: issueUUID(app), DeploymentID: issueUUID(dep), ActorAccountID: issueUUID(actor), ManifestSha256: set.Digest, CanonicalManifest: raw, MapCount: int32(len(set.Request.Manifest.Artifacts))})
	if err != nil {
		return api.IssueSourceMapsMetadata{}, mapErr(err)
	}
	return sourceMapMetadata(row), tx.Commit(ctx)
}
func (s *PgStore) GetIssueSourceMaps(ctx context.Context, account, app, dep string) (api.IssueSourceMapsMetadata, error) {
	q := sqlc.New()
	if _, err := q.IssueSourceMapDeploymentOwner(ctx, s.pool, sqlc.IssueSourceMapDeploymentOwnerParams{AccountID: issueUUID(account), AppID: issueUUID(app), DeploymentID: issueUUID(dep)}); err != nil {
		return api.IssueSourceMapsMetadata{}, mapErr(err)
	}
	row, err := q.IssueReadSourceMaps(ctx, s.pool, sqlc.IssueReadSourceMapsParams{AppID: issueUUID(app), DeploymentID: issueUUID(dep)})
	if err != nil {
		return api.IssueSourceMapsMetadata{}, mapErr(err)
	}
	return sourceMapMetadata(row), nil
}
func (s *PgStore) RetireIssueSourceMaps(ctx context.Context, account, app, dep, actor string) (api.IssueSourceMapsMetadata, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return api.IssueSourceMapsMetadata{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	owner, err := q.IssueLockApp(ctx, tx, issueUUID(app))
	if err != nil {
		return api.IssueSourceMapsMetadata{}, mapErr(err)
	}
	if issueID(owner.AccountID) != account {
		return api.IssueSourceMapsMetadata{}, ErrNotFound
	}
	row, err := q.IssueRetireSourceMaps(ctx, tx, sqlc.IssueRetireSourceMapsParams{AppID: issueUUID(app), DeploymentID: issueUUID(dep), ActorAccountID: issueUUID(actor)})
	if err != nil {
		return api.IssueSourceMapsMetadata{}, mapErr(err)
	}
	return sourceMapMetadata(row), tx.Commit(ctx)
}

func symbolicateIssueTx(ctx context.Context, q *sqlc.Queries, tx pgx.Tx, in *RecordIssueParams) error {
	if in.Event.SymbolicationStrategy != issues.DeploymentSourceMapStrategy {
		return nil
	}
	row, err := q.IssueReadSourceMaps(ctx, tx, sqlc.IssueReadSourceMapsParams{AppID: issueUUID(in.Credential.AppID), DeploymentID: issueUUID(in.Credential.DeploymentID)})
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && row.CanonicalManifest == nil) {
		return ErrIssueSourceMapsUnavailable
	}
	if err != nil {
		return err
	}
	var request api.PublishIssueSourceMapsRequest
	if err = json.Unmarshal(row.CanonicalManifest, &request); err != nil {
		return err
	}
	set, err := issues.CompileSourceMaps(request)
	if err != nil {
		return err
	}
	if set.Digest != row.ManifestSha256 {
		return ErrIssueSourceMapsUnavailable
	}
	mapped, result := set.Symbolicate(in.Event)
	event, fp, title, err := issues.Normalize(mapped, in.Now, in.Limits)
	if err != nil {
		return err
	}
	event.Redactions = append(event.Redactions, in.Event.Redactions...)
	slices.Sort(event.Redactions)
	event.Redactions = slices.Compact(event.Redactions)
	event.Symbolication = &result
	in.Event = event
	in.Fingerprint = fp
	in.Title = title
	return nil
}
