package neon

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

// Historical routing uses the source BRANCH hostname, not its ordinary
// compute hostname. Sending neon_timestamp to a compute can read today's
// primary while silently ignoring the option. Require recovery, read-only
// mode and a nonzero replay position before using a mapping as evidence.
type historicalPointReader interface {
	ReadLSN(context.Context, string) (string, error)
}

type sqlHistoricalPointReader struct{}

func (sqlHistoricalPointReader) ReadLSN(ctx context.Context, dsn string) (string, error) {
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return "", credentialSQLError(err)
	}
	defer func() { _ = conn.Close(ctx) }()
	var recovery bool
	var readOnly string
	var lsn *string
	err = conn.QueryRow(ctx, `SELECT pg_is_in_recovery(), current_setting('transaction_read_only'), pg_last_wal_replay_lsn()::text`).Scan(&recovery, &readOnly, &lsn)
	if err != nil {
		return "", credentialSQLError(err)
	}
	if !recovery || readOnly != "on" || lsn == nil {
		return "", managedpostgres.ErrUnavailable
	}
	if _, err := parseLSN(*lsn); err != nil {
		return "", err
	}
	return *lsn, nil
}

func parseLSN(value string) (uint64, error) {
	high, low, ok := strings.Cut(value, "/")
	if !ok || len(high) < 1 || len(high) > 8 || len(low) < 1 || len(low) > 8 {
		return 0, managedpostgres.ErrUnavailable
	}
	for _, part := range []string{high, low} {
		for _, c := range part {
			if !strings.ContainsRune("0123456789abcdefABCDEF", c) {
				return 0, managedpostgres.ErrUnavailable
			}
		}
	}
	hi, err := strconv.ParseUint(high, 16, 32)
	lo, lowErr := strconv.ParseUint(low, 16, 32)
	if err != nil || lowErr != nil || hi == 0 && lo == 0 {
		return 0, managedpostgres.ErrUnavailable
	}
	return hi<<32 | lo, nil
}

func historicalDSN(material managedpostgres.CredentialMaterial, primary endpoint, source resourceRef, point time.Time) (string, error) {
	if !validProviderID.MatchString(source.branchID) || !validProviderID.MatchString(primary.ID) ||
		!strings.HasPrefix(primary.ID, "ep-") || primary.BranchID != source.branchID || primary.ProjectID != source.projectID ||
		primary.Type != "read_write" || point.IsZero() || point.Nanosecond()%1000 != 0 || len(material.Endpoints) != 1 ||
		material.Endpoints[0].Role != managedpostgres.EndpointDirect || material.Endpoints[0].Host != primary.Host ||
		material.Endpoints[0].Port != 5432 {
		return "", managedpostgres.ErrUnavailable
	}
	label, suffix, ok := strings.Cut(primary.Host, ".")
	if !ok || label != primary.ID || !strings.HasSuffix(suffix, ".aws.neon.tech") ||
		strings.ContainsAny(suffix, "/:@?# \t\n") {
		return "", managedpostgres.ErrUnavailable
	}
	material.Endpoints[0].Host = source.branchID + "." + suffix
	material.TLSMode = "verify-full"
	dsn, err := probeDSN(material)
	if err != nil {
		return "", err
	}
	u, err := url.Parse(dsn)
	if err != nil {
		return "", managedpostgres.ErrUnavailable
	}
	query := u.Query()
	query.Set("options", "neon_timestamp:"+point.UTC().Format(time.RFC3339Nano))
	u.RawQuery = query.Encode()
	return u.String(), nil
}

func (p *Provider) historicalPointLSN(ctx context.Context, source resourceRef, point time.Time) (string, error) {
	if p.historicalPoints == nil || source.branchID == "" || point.IsZero() || point.After(p.now()) {
		return "", managedpostgres.ErrUnavailable
	}
	metadata, err := p.readDatabaseMetadata(ctx, source, false)
	if err != nil {
		return "", err
	}
	selected, primary, ready := selectBranch(metadata.branches.Branches, metadata.endpoints.Endpoints, source.branchID)
	if !ready || selected.ID != source.branchID || selected.ProjectID != source.projectID || selected.PendingState != "" || primary.RegionID != p.regionID {
		return "", managedpostgres.ErrUnavailable
	}
	var response connectionURIResponse
	err = p.connectionURIForDatabaseEndpoint(ctx, source.projectID, source.branchID, primary.ID, p.databaseName, ownerLogin, false, &response)
	if err != nil {
		return "", err
	}
	parsed, err := parseConnectionURI(response.URI)
	if err != nil || parsed.username != ownerLogin || parsed.database != p.databaseName {
		return "", managedpostgres.ErrUnavailable
	}
	material := managedpostgres.CredentialMaterial{Username: parsed.username, Password: parsed.password, Database: parsed.database,
		Endpoints: []managedpostgres.Endpoint{{Role: managedpostgres.EndpointDirect, Host: parsed.host, Port: parsed.port}}}
	dsn, err := historicalDSN(material, primary, source, point)
	if err != nil {
		return "", err
	}
	return p.historicalPoints.ReadLSN(ctx, dsn)
}

// A requested timestamp becomes evidence only when an independently routed
// historical source connection resolves to the target's observed parent LSN.
// Re-read the exact target after mapping; a reset or replacement cannot adopt
// the earlier observation. Neon can report the last commit's rounded timestamp
// rather than the requested instant; only an independent LSN match proves that
// earlier timestamp represents the requested state.
func (p *Provider) observeRestoredBranch(ctx context.Context, projectID, parentID, name string, target branch, request managedpostgres.RestoreRequest) (managedpostgres.ObservedDatabase, error) {
	observed, err := restoredBranchObservation(projectID, parentID, name, target, request)
	if err == nil || (!errors.Is(err, managedpostgres.ErrUnavailable) && !errors.Is(err, managedpostgres.ErrConflict)) || target.ParentLSN == "" {
		return observed, err
	}
	if !validProviderID.MatchString(target.ID) || target.Name == "" {
		return managedpostgres.ObservedDatabase{}, managedpostgres.ErrUnavailable
	}
	if target.ID == parentID || target.Name != name || target.ParentID != "" && target.ParentID != parentID ||
		target.ProjectID != "" && target.ProjectID != projectID || target.InitSource != "" && target.InitSource != "parent-data" {
		return managedpostgres.ObservedDatabase{}, managedpostgres.ErrConflict
	}
	if target.ParentTimestamp != "" {
		parentPoint, parseErr := time.Parse(time.RFC3339Nano, target.ParentTimestamp)
		if parseErr != nil || parentPoint.IsZero() {
			return managedpostgres.ObservedDatabase{}, managedpostgres.ErrUnavailable
		}
		if parentPoint.After(request.PointInTime) {
			return managedpostgres.ObservedDatabase{}, managedpostgres.ErrConflict
		}
	}
	if target.ProjectID != projectID || target.ParentID != parentID || target.InitSource != "parent-data" || target.CurrentState != "ready" || target.PendingState != "" {
		return managedpostgres.ObservedDatabase{}, managedpostgres.ErrUnavailable
	}
	created, createdErr := time.Parse(time.RFC3339Nano, target.CreatedAt)
	if createdErr != nil || created.Before(request.PointInTime) || created.After(p.now()) {
		return managedpostgres.ObservedDatabase{}, managedpostgres.ErrUnavailable
	}
	actualLSN, err := parseLSN(target.ParentLSN)
	if err != nil {
		return managedpostgres.ObservedDatabase{}, err
	}
	source := resourceRef{projectID: projectID, branchID: parentID}
	resolved, err := p.historicalPointLSN(ctx, source, request.PointInTime)
	if err != nil {
		return managedpostgres.ObservedDatabase{}, err
	}
	expectedLSN, err := parseLSN(resolved)
	if err != nil {
		return managedpostgres.ObservedDatabase{}, err
	}
	if actualLSN != expectedLSN {
		return managedpostgres.ObservedDatabase{}, managedpostgres.ErrConflict
	}
	var response createdBranchResponse
	path := "/projects/" + url.PathEscape(projectID) + "/branches/" + url.PathEscape(target.ID)
	if err := p.doJSON(ctx, http.MethodGet, path, nil, nil, &response, http.StatusOK); err != nil {
		return managedpostgres.ObservedDatabase{}, err
	}
	confirmed := response.Branch
	confirmedLSN, err := parseLSN(confirmed.ParentLSN)
	confirmedCreated, confirmedCreatedErr := time.Parse(time.RFC3339Nano, confirmed.CreatedAt)
	if err != nil || confirmed.ID != target.ID || confirmed.ProjectID != projectID || confirmed.ParentID != parentID || confirmed.Name != name ||
		confirmed.InitSource != "parent-data" || confirmed.ParentTimestamp != target.ParentTimestamp || confirmedLSN != actualLSN || confirmedCreatedErr != nil || !confirmedCreated.Equal(created) {
		return managedpostgres.ObservedDatabase{}, managedpostgres.ErrConflict
	}
	if confirmed.CurrentState != "ready" || confirmed.PendingState != "" {
		return managedpostgres.ObservedDatabase{}, managedpostgres.ErrUnavailable
	}
	return managedpostgres.ObservedDatabase{ProviderResourceID: (resourceRef{projectID: projectID, branchID: target.ID}).String(),
		DataResourceID: (resourceRef{projectID: projectID, branchID: target.ID}).String(), Status: managedpostgres.ProviderStatusPending,
		Spec: request.Spec, RestoreLineage: &managedpostgres.RestoreLineage{SourceResourceID: source.String(), PointInTime: request.PointInTime.UTC()}}, nil
}

var _ managedpostgres.RestoreInspector = (*Provider)(nil)
