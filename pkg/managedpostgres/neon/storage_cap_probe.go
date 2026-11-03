package neon

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

var _ managedpostgres.StorageCapProber = (*Provider)(nil)

// Storage pool probe timings. Neon reports a branch's logical size with a
// lag, and a new limit takes a moment to reach the compute, so the probe
// retries until the behaviour shows or its wait runs out.
const (
	storageCapProbeSizeWait  = 5 * time.Minute
	storageCapProbeLimitWait = 2 * time.Minute
	storageCapProbeHold      = time.Minute
	storageCapProbePoll      = 5 * time.Second
	storageCapProbeRaise     = 1 << 30
	// storageCapProbeWrite adds about 2 MB, enough to extend the table.
	storageCapProbeWrite = `INSERT INTO gregale_storage_probe SELECT g, repeat(md5(g::text), 32) FROM generate_series(1, 2000) AS g`
)

// ProbeStorageCap runs only in operator qualification, on a disposable
// resource (ADR-20261025094915 decisions 9 and 10). It grows the database,
// sets the limit at its reported size, checks a growing write is refused
// while reads and the compute keep working, raises the limit, checks writes
// work again, and puts the original limit back. Infrastructure failures
// return an error; a behaviour that does not match leaves its evidence field
// false.
func (p *Provider) ProbeStorageCap(ctx context.Context, providerResourceID string, material managedpostgres.CredentialMaterial) (evidence managedpostgres.StorageCapEvidence, err error) {
	ref, err := parseResourceRef(providerResourceID)
	if err != nil {
		return evidence, err
	}
	dsn, err := probeDSN(material)
	if err != nil {
		return evidence, err
	}
	before, err := p.readComputeCapProject(ctx, ref.projectID)
	if err != nil {
		return evidence, err
	}
	original := before.Settings.Quota.LogicalSizeBytes
	if original == nil || *original <= 0 {
		// There would be no limit to put back.
		return evidence, managedpostgres.ErrUnsupported
	}
	start, err := p.ObserveStorageCap(ctx, providerResourceID)
	if err != nil {
		return evidence, err
	}
	if err := storageProbeExec(ctx, dsn, `CREATE TABLE IF NOT EXISTS gregale_storage_probe (id bigint, pad text)`); err != nil {
		return evidence, err
	}
	if err := storageProbeExec(ctx, dsn, storageCapProbeWrite); err != nil {
		return evidence, err
	}
	grown, reported := p.awaitStorageSize(ctx, providerResourceID, start.SizeBytes)
	evidence.SizeReported = reported
	if !reported {
		return evidence, nil
	}
	defer func() {
		// The qualification checks after this probe compare the resource
		// with its spec, so the original limit always goes back.
		restoreErr := p.SetStorageCap(context.WithoutCancel(ctx), providerResourceID, *original)
		if err == nil {
			err = restoreErr
		}
	}()
	if err := p.SetStorageCap(ctx, providerResourceID, max(grown.SizeBytes, 1)); err != nil {
		return evidence, err
	}
	evidence.WritesRefusedAtLimit = awaitStorageWrite(ctx, dsn, false, storageCapProbeLimitWait)
	evidence.ReadsAllowedAtLimit = storageProbeRead(ctx, dsn) == nil
	evidence.ComputeKeptRunning = evidence.ReadsAllowedAtLimit && p.computeKeptRunning(ctx, providerResourceID, dsn, storageCapProbeHold)
	if err := p.SetStorageCap(ctx, providerResourceID, grown.SizeBytes+storageCapProbeRaise); err != nil {
		return evidence, err
	}
	evidence.WritesAllowedAfterRaise = awaitStorageWrite(ctx, dsn, true, storageCapProbeLimitWait)
	final, err := p.readComputeCapProject(ctx, ref.projectID)
	if err != nil {
		return evidence, err
	}
	evidence.ComputeQuotaPreserved = sameQuota(final.Settings.Quota.ComputeTimeSeconds, before.Settings.Quota.ComputeTimeSeconds)
	return evidence, nil
}

// awaitStorageSize waits until the branch's reported size grows past start.
func (p *Provider) awaitStorageSize(ctx context.Context, providerResourceID string, start int64) (managedpostgres.StorageCapObservation, bool) {
	deadline := time.Now().Add(storageCapProbeSizeWait)
	for {
		observed, err := p.ObserveStorageCap(ctx, providerResourceID)
		if err == nil && observed.SizeBytes > start {
			return observed, true
		}
		if time.Now().After(deadline) || sleepContext(ctx, storageCapProbePoll) != nil {
			return managedpostgres.StorageCapObservation{}, false
		}
	}
}

// awaitStorageWrite retries a growing write until it succeeds (allowed) or
// is refused for the size limit (not allowed). Any other failure, such as a
// refused connection, proves neither.
func awaitStorageWrite(ctx context.Context, dsn string, allowed bool, wait time.Duration) bool {
	deadline := time.Now().Add(wait)
	for {
		attempt, cancel := context.WithTimeout(ctx, 30*time.Second)
		err := storageProbeExec(attempt, dsn, storageCapProbeWrite)
		cancel()
		if (allowed && err == nil) || (!allowed && storageLimitRefusal(err)) {
			return true
		}
		if time.Now().After(deadline) || sleepContext(ctx, storageCapProbePoll) != nil {
			return false
		}
	}
}

// storageLimitRefusal reports a server error for the project size limit:
// Neon fails the file extension with disk_full (53100).
func storageLimitRefusal(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && (pgErr.Code == "53100" || strings.Contains(strings.ToLower(pgErr.Message), "size limit"))
}

// computeKeptRunning reads every poll for hold and checks the compute is
// never reported suspended.
func (p *Provider) computeKeptRunning(ctx context.Context, providerResourceID, dsn string, hold time.Duration) bool {
	deadline := time.Now().Add(hold)
	for {
		observed, err := p.Inspect(ctx, providerResourceID)
		if err != nil || observed.ComputeState == managedpostgres.ComputeStateSuspended || storageProbeRead(ctx, dsn) != nil {
			return false
		}
		if time.Now().After(deadline) {
			return true
		}
		if sleepContext(ctx, storageCapProbePoll) != nil {
			return false
		}
	}
}

func storageProbeRead(ctx context.Context, dsn string) error {
	attempt, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	conn, err := pgx.Connect(attempt, dsn)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close(attempt) }()
	var rows int64
	return conn.QueryRow(attempt, "SELECT count(*) FROM gregale_storage_probe").Scan(&rows)
}

func storageProbeExec(ctx context.Context, dsn, statement string) error {
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close(ctx) }()
	_, err = conn.Exec(ctx, statement)
	return err
}
