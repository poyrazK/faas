package state

import (
	"context"
	"regexp"
	"strings"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// ManagedPostgresEgressEndpoint is one managed database endpoint an app is
// bound to (ADR-20261026150000): the bridge resolver opens Port to Host's
// addresses for the app's instances on every plan.
type ManagedPostgresEgressEndpoint struct {
	AppID string
	Host  string
	Port  int
}

var managedPostgresEndpointHost = regexp.MustCompile(`^[a-z0-9]([a-z0-9.-]*[a-z0-9])?$`)

// RecordManagedPostgresDatabaseEndpoint stores a host name and port a
// database's credentials connect to. Host names are lower-cased; IP literals
// are not recorded, since the resolver opens ports only to resolved names.
func (s *PgStore) RecordManagedPostgresDatabaseEndpoint(ctx context.Context, databaseID, host string, port int) error {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	id, err := parsePgUUID(databaseID)
	if err != nil || len(host) > 253 || !managedPostgresEndpointHost.MatchString(host) || port < 1 || port > 65535 {
		return ErrInvalidArgument
	}
	return mapErr(new(sqlc.Queries).RecordManagedPostgresDatabaseEndpoint(ctx, s.pool, sqlc.RecordManagedPostgresDatabaseEndpointParams{DatabaseID: id, Host: host, Port: int32(port)}))
}

// ManagedPostgresEgressEndpoints lists every ready or retiring binding's
// database endpoints with the bound app.
func (s *PgStore) ManagedPostgresEgressEndpoints(ctx context.Context) ([]ManagedPostgresEgressEndpoint, error) {
	rows, err := new(sqlc.Queries).ListManagedPostgresEgressEndpoints(ctx, s.pool)
	if err != nil {
		return nil, mapErr(err)
	}
	out := make([]ManagedPostgresEgressEndpoint, 0, len(rows))
	for _, row := range rows {
		out = append(out, ManagedPostgresEgressEndpoint{AppID: uuidString(row.AppID), Host: row.Host, Port: int(row.Port)})
	}
	return out, nil
}
