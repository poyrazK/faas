package db

import (
	"context"
	"strings"

	faasschema "github.com/onebox-faas/faas"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/db/migrationsqlc"
)

func migrationRecoverySources() ([]migrations.Source, string, error) {
	digest, err := migrations.SourceDigest()
	if err != nil {
		return nil, "", err
	}
	if digest != strings.TrimSpace(faasschema.MigrationSourceSHA256()) {
		return nil, "", ErrMigrationRecoverySource
	}
	sources, err := migrations.Sources()
	return sources, digest, err
}

func migrationRecoveryHistory(rows []migrationsqlc.GooseDbVersion, sources []migrations.Source) (map[int64]migrationsqlc.GooseDbVersion, error) {
	known := make(map[int64]bool, len(sources))
	for _, source := range sources {
		known[source.Version] = true
	}
	latest := make(map[int64]migrationsqlc.GooseDbVersion, len(rows))
	for _, row := range rows {
		if row.VersionID == 0 {
			continue
		}
		if !known[row.VersionID] || !row.Tstamp.Valid {
			return nil, ErrMigrationRecoveryHistory
		}
		if old, ok := latest[row.VersionID]; !ok || row.ID > old.ID {
			latest[row.VersionID] = row
		}
	}
	for _, source := range sources {
		row, found := latest[source.Version]
		if (found && !row.IsApplied) || (!found && !migrations.IsTimestampMigrationVersion(source.Version)) {
			return nil, ErrMigrationRecoveryHistory
		}
	}
	return latest, nil
}

func migrationRecoveryMissing(plan *MigrationLedgerRecoveryPlan, latest map[int64]migrationsqlc.GooseDbVersion, sources []migrations.Source) error {
	eligible, err := migrations.ApplicationStandardRecoverySources()
	if err != nil {
		return err
	}
	for _, source := range sources {
		if _, found := latest[source.Version]; found {
			continue
		}
		if candidate, ok := eligible[source.Version]; ok {
			plan.Repair = append(plan.Repair, candidate)
		} else {
			plan.Remaining = append(plan.Remaining, source)
		}
	}
	if len(plan.Repair) == 0 {
		return ErrMigrationRecoveryNoCandidates
	}
	return nil
}

func migrationRecoveryTarget(ctx context.Context, db migrationsqlc.DBTX) (string, error) {
	identity, err := migrationsqlc.New().ReadMigrationRecoveryIdentity(ctx, db)
	if err != nil {
		return "", err
	}
	if identity.SchemaName != "public" || identity.ServerVersion/10000 != 16 || !identity.DatabaseOid.Valid {
		return "", ErrMigrationRecoveryTransport
	}
	cluster, err := migrationsqlc.New().ReadMigrationRecoveryCluster(ctx, db)
	if err != nil {
		return "", err
	}
	if cluster == "" {
		return "", ErrMigrationRecoverySchema
	}
	return migrationRecoveryDigest(struct {
		Cluster             string
		Database            uint32
		Name, Schema, Actor string
	}{cluster, identity.DatabaseOid.Uint32, identity.DatabaseName, identity.SchemaName, identity.Actor})
}
