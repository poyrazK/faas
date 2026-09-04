package migrations

import "time"

const (
	// LegacyMigrationMaxVersion is the final migration in Gregale's original
	// globally sequential namespace. Versions 1..590 remain immutable and
	// contiguous so existing databases and old binaries keep their historical
	// guarantees.
	LegacyMigrationMaxVersion int64 = 590

	// TimestampMigrationMinVersion is the cutover marker for independently
	// authored migrations. New versions use YYYYMMDDHHMMSSmmm in UTC, which
	// keeps filenames sortable while making cross-PR collisions vanishingly
	// unlikely without reserving a global slot.
	TimestampMigrationMinVersion    int64 = 20_260_904_000_000_000
	TimestampMigrationVersionDigits int   = 17
)

// TimestampMigrationVersion returns a UTC YYYYMMDDHHMMSSmmm migration ID.
func TimestampMigrationVersion(now time.Time) int64 {
	utc := now.UTC()
	date := int64(utc.Year())
	date = date*100 + int64(utc.Month())
	date = date*100 + int64(utc.Day())
	date = date*100 + int64(utc.Hour())
	date = date*100 + int64(utc.Minute())
	date = date*100 + int64(utc.Second())
	return date*1000 + int64(utc.Nanosecond()/int(time.Millisecond))
}

// IsTimestampMigrationVersion reports whether version belongs to the
// post-cutover namespace whose migrations may be applied out of order.
func IsTimestampMigrationVersion(version int64) bool {
	return version >= TimestampMigrationMinVersion
}
