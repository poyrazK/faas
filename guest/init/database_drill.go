package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Restore drill marks (ADR-1027). Before a drill restores a database, the
// scheduler has the database commit the drill's id into a one-row table
// in the maintenance database, and recovers to just after that commit;
// the restored copy must then hold the id. The schema and table belong to
// the superuser and grant nothing to anyone, so no customer role reads
// or changes them; they are a few bytes and survive pg_upgrade like any
// table.

// drillMarkSQL commits drill's mark, replacing the previous one, then
// reads the clock in microseconds since the epoch: after the commit, so a
// recovery to that instant includes it. The mark waits for no standby:
// it needs no more durability than the archive gives it. drill is a
// validated lowercase UUID.
func drillMarkSQL(drill string) string {
	return "BEGIN;" +
		" SET LOCAL synchronous_commit = local;" +
		" CREATE SCHEMA IF NOT EXISTS gregale;" +
		" CREATE TABLE IF NOT EXISTS gregale.restore_drill_mark (singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton)," +
		" drill_id uuid NOT NULL, marked_at timestamptz NOT NULL);" +
		" INSERT INTO gregale.restore_drill_mark (drill_id, marked_at) VALUES ('" + drill + "', clock_timestamp())" +
		" ON CONFLICT (singleton) DO UPDATE SET drill_id = EXCLUDED.drill_id, marked_at = EXCLUDED.marked_at;" +
		" COMMIT;" +
		" SELECT (extract(epoch FROM clock_timestamp()) * 1000000)::bigint"
}

// drillTableQuery reports whether the database has a mark table (t or f);
// drillMarkQuery reads its mark.
const (
	drillTableQuery = "SELECT to_regclass('gregale.restore_drill_mark') IS NOT NULL"
	drillMarkQuery  = "SELECT drill_id FROM gregale.restore_drill_mark"
)

// parseDrillClock reads drillMarkSQL's clock.
func parseDrillClock(out string) (time.Time, error) {
	micros, err := strconv.ParseInt(strings.TrimSpace(out), 10, 64)
	if err != nil || micros <= 0 {
		return time.Time{}, fmt.Errorf("drill mark: clock %q", out)
	}
	return time.UnixMicro(micros).UTC(), nil
}
