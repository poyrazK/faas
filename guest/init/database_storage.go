package main

// The storage guard (ADR-1027): a primary's volume is its storage limit,
// and a full volume makes PostgreSQL PANIC on its next WAL write, then
// crash-loop. Well before that, the guard turns the database read-only
// (default_transaction_read_only, which an application cannot miss and an
// operator can override to delete data), and turns it writable again once
// enough space is free, after a delete or a storage resize. A ballast
// file holds back a little space, which the guard gives up if a session
// that overrode read-only keeps writing.

// storageReadOnlyBelow is the free space under which a primary turns
// read-only: 5% of the volume, at least 128 MiB and at most 2 GiB.
func storageReadOnlyBelow(total uint64) uint64 {
	return min(max(total/20, 128<<20), 2<<30)
}

// storageWritableAbove is the free space at which it turns writable again.
func storageWritableAbove(total uint64) uint64 { return 2 * storageReadOnlyBelow(total) }

// storageBallastBytes is the ballast file's size: at most half of
// storageReadOnlyBelow, so restoring it never turns the database
// read-only again.
func storageBallastBytes(total uint64) uint64 { return min(total/32, 64<<20) }

// storageBallastReleaseBelow is the free space under which the ballast is
// given up.
const storageBallastReleaseBelow = 32 << 20

// nextStorageReadOnly is the mode a primary should be in, with hysteresis
// between storageReadOnlyBelow and storageWritableAbove.
func nextStorageReadOnly(readOnly bool, free, total uint64) bool {
	switch {
	case !readOnly && free < storageReadOnlyBelow(total):
		return true
	case readOnly && free >= storageWritableAbove(total):
		return false
	}
	return readOnly
}

// storageModeStatements switch the server's default; every session
// without its own override follows on the reload. ALTER SYSTEM runs
// outside a transaction, so each is its own statement.
func storageModeStatements(readOnly bool) []string {
	set := "ALTER SYSTEM RESET default_transaction_read_only"
	if readOnly {
		set = "ALTER SYSTEM SET default_transaction_read_only = on"
	}
	return []string{set, "SELECT pg_catalog.pg_reload_conf()"}
}

// storageModeQuery reads the read-only default the server's files set,
// which a session's own options cannot mask.
const storageModeQuery = `SELECT coalesce(bool_or(setting::bool) FILTER (WHERE applied), false) FROM pg_catalog.pg_file_settings
 WHERE name = 'default_transaction_read_only'`

// databaseAdminOptions exempts the guest's own superuser sessions from the
// read-only default, so credentials, replication and imports' bookkeeping
// still work while a database is read-only.
const databaseAdminOptions = "PGOPTIONS=-c default_transaction_read_only=off"
