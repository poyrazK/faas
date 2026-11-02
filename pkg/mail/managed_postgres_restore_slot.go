package mail

import (
	"fmt"
	"regexp"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// Managed PostgreSQL restore slot notices (ADR-20261031123259).
const (
	ManagedPostgresRestoreSlotEnding  = "ending"
	ManagedPostgresRestoreSlotExpired = "expired"
)

// ManagedPostgresRestoreSlotNotice is the frozen snapshot one restore slot
// email reports. RestorableUntil is set only for an expired copy.
type ManagedPostgresRestoreSlotNotice struct {
	Kind            string
	DatabaseName    string
	SlotUntil       time.Time
	RestorableUntil time.Time
}

// managedPostgresDatabaseName is the database table's name CHECK.
var managedPostgresDatabaseName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)

// ManagedPostgresRestoreSlotBodyV1 renders one restore slot email.
func ManagedPostgresRestoreSlotBodyV1(email string, n ManagedPostgresRestoreSlotNotice) (subject, body string, ok bool) {
	if !managedPostgresDatabaseName.MatchString(n.DatabaseName) || n.SlotUntil.IsZero() {
		return "", "", false
	}
	until := n.SlotUntil.UTC().Format("2006-01-02 15:04 UTC")
	var detail string
	switch n.Kind {
	case ManagedPostgresRestoreSlotEnding:
		if !n.RestorableUntil.IsZero() {
			return "", "", false
		}
		subject = fmt.Sprintf("Your restored database %s will be deleted on %s", n.DatabaseName, n.SlotUntil.UTC().Format("2006-01-02"))
		detail = fmt.Sprintf(`The restore that created %s took your plan's one extra database, which lasts until %s.
Your plan has no room for it yet. To keep it, delete another database or upgrade your plan before then.
Otherwise its delete is scheduled at that time; you can still recover it for %d days after, once you have room.`, n.DatabaseName, until,
			int(api.ManagedPostgresDeleteGracePeriod/(24*time.Hour)))
	case ManagedPostgresRestoreSlotExpired:
		if n.RestorableUntil.IsZero() {
			return "", "", false
		}
		subject = fmt.Sprintf("Your restored database %s is scheduled for deletion", n.DatabaseName)
		detail = fmt.Sprintf(`The restore that created %s took your plan's one extra database, which ended at %s with no room on your plan.
Its app bindings were removed and its delete was scheduled. To recover it before %s, delete another database or upgrade your plan, then run gregale postgres recover %s.
After that, it is deleted for good.`, n.DatabaseName, until, n.RestorableUntil.UTC().Format("2006-01-02 15:04 UTC"), n.DatabaseName)
	default:
		return "", "", false
	}
	body = fmt.Sprintf(`Hi,

This is about a managed PostgreSQL database of your Gregale account (%s).
%s

See your databases with gregale postgres list or on the PostgreSQL page of the dashboard.

— Gregale
`, safeRecipient(email), detail)
	return subject, body, true
}
