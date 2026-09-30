// adr: 375
package state

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"math"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// PublicHostPolicyReader exposes only the routing projection. Account
// credentials, application env and deployment artifacts are never loaded.
type PublicHostPolicyReader interface {
	AppByID(context.Context, string) (App, error)
	AppBySlug(context.Context, string) (App, error)
	AccountByID(context.Context, string) (Account, error)
	DeploymentByID(context.Context, string) (Deployment, error)
	DeploymentByRevision(context.Context, string, int) (Deployment, error)
	LiveDeploymentForScope(context.Context, string, string) (Deployment, error)
	LiveDeploymentsForScope(context.Context, string, string) ([]Deployment, error)
	ProjectEnvironmentByID(context.Context, string) (ProjectEnvironment, error)
	GetProjectEnvironmentEdgePolicy(context.Context, string, string, string) (ProjectEnvironmentEdgePolicy, error)
	ActiveProjectReleaseSet(context.Context, string, string, string) (ProjectReleaseSet, error)
	DeploymentAliasByHostLabel(context.Context, string) (DeploymentAlias, error)
	DomainByName(context.Context, string) (CustomDomain, error)
	WildcardDomainForHost(context.Context, string) (CustomDomain, error)
	TenantSurfaceByHostname(context.Context, string) (TenantSurface, error)
	GetTenantHostnameByName(context.Context, string) (TenantHostname, error)
	PlatformTenantHostBinding(context.Context, string) (PlatformTenantHostBinding, error)
	PublicHostPolicyRevision() string
}

type PublicHostPolicySnapshotStore interface {
	WithPublicHostPolicySnapshot(context.Context, func(PublicHostPolicyReader) error) error
}

// The routing verifier uses the same transaction as eligibility and weights.
type PublicRoutingHostPolicyReader interface {
	HostPolicyReader() PublicHostPolicyReader
}

func (s *PgStore) WithPublicHostPolicySnapshot(ctx context.Context, read func(PublicHostPolicyReader) error) error {
	if read == nil {
		return ErrInvalidArgument
	}
	return s.WithServicePolicySnapshot(ctx, func(reader ServicePolicyReader) error {
		return read(newPublicHostPolicyReader(reader.(servicePolicyReader).tx))
	})
}

type publicHostPolicyReader struct {
	tx       pgx.Tx
	revision hash.Hash
}

func newPublicHostPolicyReader(tx pgx.Tx) *publicHostPolicyReader {
	return &publicHostPolicyReader{tx: tx, revision: sha256.New()}
}

func (s *publicHostPolicyReader) PublicHostPolicyRevision() string {
	return "public-host-v1:" + hex.EncodeToString(s.revision.Sum(nil))
}

// Framed JSON records include successful reads and authoritative
// misses. A claimed hostname cannot silently become a different fallback.
func (s *publicHostPolicyReader) record(key string, data []byte, err error) {
	if err != nil && !errors.Is(mapErr(err), ErrNotFound) {
		return
	}
	encoded, _ := json.Marshal(struct {
		Key  string
		Data json.RawMessage
	}{key, data})
	_, _ = s.revision.Write(encoded)
}

func decodePublicHostJSON[T any](data []byte, err error) (T, error) {
	var result T
	if err != nil {
		return result, mapErr(err)
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return result, fmt.Errorf("decode public host policy: %w", err)
	}
	return result, nil
}

func (s *publicHostPolicyReader) AppByID(ctx context.Context, id string) (App, error) {
	data, err := sqlc.New().ReadPublicHostApp(ctx, s.tx, sqlc.ReadPublicHostAppParams{AppID: uuidToPgtype(id)})
	s.record("app-id:"+id, data, err)
	return decodePublicHostJSON[App](data, err)
}

func (s *publicHostPolicyReader) AppBySlug(ctx context.Context, slug string) (App, error) {
	data, err := sqlc.New().ReadPublicHostApp(ctx, s.tx, sqlc.ReadPublicHostAppParams{Slug: slug})
	s.record("app-slug:"+slug, data, err)
	return decodePublicHostJSON[App](data, err)
}

func (s *publicHostPolicyReader) AccountByID(ctx context.Context, id string) (Account, error) {
	data, err := sqlc.New().ReadPublicHostAccount(ctx, s.tx, uuidToPgtype(id))
	s.record("account:"+id, data, err)
	return decodePublicHostJSON[Account](data, err)
}

func (s *publicHostPolicyReader) deployments(ctx context.Context, params sqlc.ReadPublicHostDeploymentParams) ([]Deployment, error) {
	rows, err := sqlc.New().ReadPublicHostDeployment(ctx, s.tx, params)
	if err != nil {
		return nil, mapErr(err)
	}
	if len(rows) > api.TrafficPolicyMaxDeployments {
		return nil, errors.New("public host deployment limit exceeded")
	}
	key, _ := json.Marshal(params)
	encoded, _ := json.Marshal(rows)
	s.record("deployments:"+string(key), encoded, nil)
	result := make([]Deployment, 0, len(rows))
	for _, data := range rows {
		deployment, err := decodePublicHostJSON[Deployment](data, nil)
		if err != nil {
			return nil, err
		}
		result = append(result, deployment)
	}
	return result, nil
}

func onePublicHostDeployment(rows []Deployment, err error) (Deployment, error) {
	if err != nil {
		return Deployment{}, err
	}
	if len(rows) == 0 {
		return Deployment{}, ErrNotFound
	}
	if len(rows) != 1 {
		return Deployment{}, ErrConflict
	}
	return rows[0], nil
}

func (s *publicHostPolicyReader) DeploymentByID(ctx context.Context, id string) (Deployment, error) {
	return onePublicHostDeployment(s.deployments(ctx, sqlc.ReadPublicHostDeploymentParams{DeploymentID: uuidToPgtype(id), RowLimit: 2}))
}

func (s *publicHostPolicyReader) DeploymentByRevision(ctx context.Context, app string, revision int) (Deployment, error) {
	if revision <= 0 || revision > math.MaxInt32 {
		return Deployment{}, ErrNotFound
	}
	return onePublicHostDeployment(s.deployments(ctx, sqlc.ReadPublicHostDeploymentParams{AppID: uuidToPgtype(app), Revision: int32(revision), RowLimit: 2}))
}

func (s *publicHostPolicyReader) LiveDeploymentForScope(ctx context.Context, app, scope string) (Deployment, error) {
	return onePublicHostDeployment(s.deployments(ctx, sqlc.ReadPublicHostDeploymentParams{AppID: uuidToPgtype(app), Scope: scope, RowLimit: 1}))
}

func (s *publicHostPolicyReader) LiveDeploymentsForScope(ctx context.Context, app, scope string) ([]Deployment, error) {
	return s.deployments(ctx, sqlc.ReadPublicHostDeploymentParams{AppID: uuidToPgtype(app), Scope: scope, RowLimit: int32(api.TrafficPolicyMaxDeployments + 1)})
}

func (s *publicHostPolicyReader) ProjectEnvironmentByID(ctx context.Context, id string) (ProjectEnvironment, error) {
	data, err := sqlc.New().ReadPublicHostEnvironment(ctx, s.tx, uuidToPgtype(id))
	s.record("environment:"+id, data, err)
	return decodePublicHostJSON[ProjectEnvironment](data, err)
}

func (s *publicHostPolicyReader) GetProjectEnvironmentEdgePolicy(ctx context.Context, account, app, scope string) (ProjectEnvironmentEdgePolicy, error) {
	data, err := sqlc.New().ReadPublicHostEnvironmentPolicy(ctx, s.tx, sqlc.ReadPublicHostEnvironmentPolicyParams{
		AccountID: uuidToPgtype(account), AppID: uuidToPgtype(app), Scope: scope})
	s.record("environment-policy:"+account+":"+app+":"+scope, data, err)
	return decodePublicHostJSON[ProjectEnvironmentEdgePolicy](data, err)
}

func (s *publicHostPolicyReader) ActiveProjectReleaseSet(ctx context.Context, account, project, scope string) (ProjectReleaseSet, error) {
	data, err := sqlc.New().ReadProjectReleaseSet(ctx, s.tx, sqlc.ReadProjectReleaseSetParams{
		AccountID: uuidToPgtype(account), ProjectID: uuidToPgtype(project), Environment: scope})
	s.record("release:"+account+":"+project+":"+scope, data, err)
	return decodePublicHostJSON[ProjectReleaseSet](data, err)
}

func (s *publicHostPolicyReader) DeploymentAliasByHostLabel(ctx context.Context, label string) (DeploymentAlias, error) {
	rows, err := sqlc.New().DeploymentAliasByHostLabel(ctx, s.tx, label)
	if err != nil {
		return DeploymentAlias{}, mapErr(err)
	}
	encoded, _ := json.Marshal(rows)
	s.record("alias:"+label, encoded, nil)
	if len(rows) == 0 {
		return DeploymentAlias{}, ErrNotFound
	}
	if len(rows) != 1 {
		return DeploymentAlias{}, ErrConflict
	}
	row := rows[0]
	return DeploymentAlias{AppID: pgUUIDString(row.AppID), Name: row.Name, DeploymentID: pgUUIDString(row.DeploymentID), Revision: int(row.Revision)}, nil
}

func (s *publicHostPolicyReader) domain(ctx context.Context, host string, wildcard bool) (CustomDomain, error) {
	data, err := sqlc.New().ReadPublicHostDomain(ctx, s.tx, sqlc.ReadPublicHostDomainParams{Host: host, Wildcard: wildcard})
	s.record(fmt.Sprintf("domain:%t:%s", wildcard, host), data, err)
	return decodePublicHostJSON[CustomDomain](data, err)
}

func (s *publicHostPolicyReader) DomainByName(ctx context.Context, host string) (CustomDomain, error) {
	return s.domain(ctx, host, false)
}

func (s *publicHostPolicyReader) WildcardDomainForHost(ctx context.Context, host string) (CustomDomain, error) {
	return s.domain(ctx, host, true)
}

func (s *publicHostPolicyReader) TenantSurfaceByHostname(ctx context.Context, host string) (TenantSurface, error) {
	data, err := sqlc.New().ReadPublicHostTenantSurface(ctx, s.tx, host)
	s.record("surface:"+host, data, err)
	return decodePublicHostJSON[TenantSurface](data, err)
}

func (s *publicHostPolicyReader) GetTenantHostnameByName(ctx context.Context, host string) (TenantHostname, error) {
	data, err := sqlc.New().ReadPublicHostTenantHostname(ctx, s.tx, host)
	s.record("tenant-hostname:"+host, data, err)
	return decodePublicHostJSON[TenantHostname](data, err)
}

func (s *publicHostPolicyReader) PlatformTenantHostBinding(ctx context.Context, host string) (PlatformTenantHostBinding, error) {
	data, err := sqlc.New().ReadPublicHostTenantBinding(ctx, s.tx, host)
	s.record("tenant-binding:"+host, data, err)
	return decodePublicHostJSON[PlatformTenantHostBinding](data, err)
}
