//go:build linux

package main

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/dbguest"
)

// A physical base backup copies PGDATA. Keep the override on the volume
// outside PGDATA so a restored or seeded database takes its own policy.
const databaseAutovacuumFile = databaseMountPoint + "/gregale-autovacuum.conf"

func (g *databaseGuest) autovacuum(req dbguest.Request, response dbguest.Response) dbguest.Response {
	if response.State != dbguest.StateReady && response.State != dbguest.StateStandby {
		response.Error = dbguest.ErrCodeNotReady
		return response
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2500*time.Millisecond)
	defer cancel()
	query := func(ctx context.Context, sql string) (string, error) {
		return g.queryIn(ctx, "postgres", sql)
	}
	var settings dbguest.AutovacuumSettings
	var err error
	if req.AutovacuumProfile == "" {
		settings, err = readAutovacuum(ctx, query)
	} else {
		config := autovacuumConfig{pgData: databasePGData, file: databaseAutovacuumFile, uid: g.uid, gid: g.gid}
		settings, err = config.apply(ctx, req.AutovacuumProfile, req.AutovacuumRevision, query)
	}
	if err != nil {
		response.Error = dbguest.ErrCodeNotReady
		return response
	}
	response.Autovacuum = &settings
	return response
}
