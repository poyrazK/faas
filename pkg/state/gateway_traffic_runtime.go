// adr: 531
package state

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// ErrGatewayTrafficEpochLost fences replaced processes and unregistered nodes.
var ErrGatewayTrafficEpochLost = errors.New("gateway traffic observation generation lost")

type GatewayTrafficEpoch struct {
	NodeName   string
	Generation int64
	BootID     string
}

// GatewayTrafficFeatures describes wiring, not successful request enforcement.
// RetryEnabled is the public edge retry gate; service retries have their own policy.
type GatewayTrafficFeatures struct {
	RetryEnabled       bool
	RateCounterMode    string
	RetryCounterMode   string
	RetryBackendID     string
	DeadlineSigning    bool
	PolicySnapshot     bool
	SecurityRevocation bool
	ManagedHTTP        bool
	ManagedCircuit     bool
}

type ServingGatewayTrafficRuntime struct {
	NodeName    string
	Generation  int64
	ReportedAt  time.Time
	DatabaseNow time.Time
	GatewayTrafficFeatures
}

func (s *PgStore) ReadGatewayTrafficEpoch(ctx context.Context, node string) (GatewayTrafficEpoch, error) {
	row, err := sqlc.New().ReadGatewayTrafficRuntimeEpoch(ctx, s.pool, node)
	if errors.Is(err, pgx.ErrNoRows) {
		return GatewayTrafficEpoch{NodeName: node}, nil
	}
	if err != nil {
		return GatewayTrafficEpoch{}, fmt.Errorf("read gateway traffic generation: %w", err)
	}
	return GatewayTrafficEpoch{NodeName: node, Generation: row.Generation, BootID: uuidString(row.BootID)}, nil
}

// RegisterGatewayTrafficEpoch uses a baseline read once by the new process.
// Retrying the same boot/baseline can recover a committed, lost response, but
// cannot take ownership back from a replacement process.
func (s *PgStore) RegisterGatewayTrafficEpoch(ctx context.Context, node, boot string, expected int64) (GatewayTrafficEpoch, error) {
	id, err := parsePgUUID(boot)
	if err != nil || node == "" || expected < 0 {
		return GatewayTrafficEpoch{}, errors.New("invalid gateway traffic registration")
	}
	current, err := s.ReadGatewayTrafficEpoch(ctx, node)
	if err != nil {
		return GatewayTrafficEpoch{}, err
	}
	if current.BootID == boot {
		return current, nil
	}
	generation, err := sqlc.New().RegisterGatewayTrafficRuntimeEpoch(ctx, s.pool, sqlc.RegisterGatewayTrafficRuntimeEpochParams{
		NodeName: node, BootID: id, ExpectedGeneration: expected,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return GatewayTrafficEpoch{}, ErrGatewayTrafficEpochLost
	}
	if err != nil {
		return GatewayTrafficEpoch{}, fmt.Errorf("register gateway traffic generation: %w", err)
	}
	return GatewayTrafficEpoch{NodeName: node, Generation: generation, BootID: boot}, nil
}

var gatewayTrafficBackendID = regexp.MustCompile(`^[0-9a-f]{16}$`)

func (f GatewayTrafficFeatures) validate() error {
	if f.RateCounterMode != "central" && f.RateCounterMode != "local" && f.RateCounterMode != "unwired" {
		return errors.New("invalid gateway traffic rate counter mode")
	}
	if f.RetryCounterMode != "shared" && f.RetryCounterMode != "redis" && f.RetryCounterMode != "local" && f.RetryCounterMode != "unwired" {
		return errors.New("invalid gateway traffic retry counter mode")
	}
	shared := f.RetryCounterMode == "shared" || f.RetryCounterMode == "redis"
	if (shared && !gatewayTrafficBackendID.MatchString(f.RetryBackendID)) || (!shared && f.RetryBackendID != "") {
		return errors.New("invalid gateway traffic retry backend identity")
	}
	if f.ManagedCircuit && !f.ManagedHTTP {
		return errors.New("managed circuit requires managed HTTP wiring")
	}
	return nil
}

func (s *PgStore) ReportGatewayTrafficRuntime(ctx context.Context, epoch GatewayTrafficEpoch, f GatewayTrafficFeatures) error {
	if err := f.validate(); err != nil {
		return err
	}
	id, err := parsePgUUID(epoch.BootID)
	if err != nil || epoch.Generation <= 0 || epoch.NodeName == "" {
		return errors.New("invalid gateway traffic generation")
	}
	n, err := sqlc.New().ReportGatewayTrafficRuntime(ctx, s.pool, sqlc.ReportGatewayTrafficRuntimeParams{
		NodeName: epoch.NodeName, Generation: epoch.Generation, BootID: id,
		RetryEnabled: f.RetryEnabled, RateCounterMode: f.RateCounterMode, RetryCounterMode: f.RetryCounterMode,
		RetryBackendID: f.RetryBackendID, DeadlineSigning: f.DeadlineSigning, PolicySnapshot: f.PolicySnapshot,
		SecurityRevocation: f.SecurityRevocation, ManagedHttp: f.ManagedHTTP, ManagedCircuit: f.ManagedCircuit,
	})
	return gatewayTrafficWriteResult(n, err)
}

func (s *PgStore) RetireGatewayTrafficRuntime(ctx context.Context, epoch GatewayTrafficEpoch) error {
	id, err := parsePgUUID(epoch.BootID)
	if err != nil || epoch.Generation <= 0 || epoch.NodeName == "" {
		return errors.New("invalid gateway traffic generation")
	}
	n, err := sqlc.New().RetireGatewayTrafficRuntime(ctx, s.pool, sqlc.RetireGatewayTrafficRuntimeParams{
		NodeName: epoch.NodeName, Generation: epoch.Generation, BootID: id,
	})
	return gatewayTrafficWriteResult(n, err)
}

func gatewayTrafficWriteResult(n int64, err error) error {
	if err != nil {
		return fmt.Errorf("write gateway traffic observation: %w", err)
	}
	if n != 1 {
		return ErrGatewayTrafficEpochLost
	}
	return nil
}

func (s *PgStore) ListServingGatewayTrafficRuntime(ctx context.Context) ([]ServingGatewayTrafficRuntime, error) {
	rows, err := sqlc.New().ListServingGatewayTrafficRuntime(ctx, s.pool, api.TrafficRuntimeObservationMaxNodes+1)
	if err != nil {
		return nil, fmt.Errorf("list gateway traffic observations: %w", err)
	}
	if len(rows) > api.TrafficRuntimeObservationMaxNodes {
		return nil, errors.New("gateway traffic observation roster exceeds limit")
	}
	out := make([]ServingGatewayTrafficRuntime, len(rows))
	for i, row := range rows {
		out[i] = ServingGatewayTrafficRuntime{
			NodeName: row.NodeName, Generation: row.Generation, DatabaseNow: row.DatabaseNow.Time,
			GatewayTrafficFeatures: GatewayTrafficFeatures{
				RetryEnabled: row.RetryEnabled, RateCounterMode: row.RateCounterMode, RetryCounterMode: row.RetryCounterMode,
				RetryBackendID: row.RetryBackendID, DeadlineSigning: row.DeadlineSigning, PolicySnapshot: row.PolicySnapshot,
				SecurityRevocation: row.SecurityRevocation, ManagedHTTP: row.ManagedHttp, ManagedCircuit: row.ManagedCircuit,
			},
		}
		if row.ReportedAt.Valid {
			out[i].ReportedAt = row.ReportedAt.Time
		}
	}
	return out, nil
}
