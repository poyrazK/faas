package mail

import (
	"fmt"
	"time"
)

// Managed PostgreSQL databases suspended past a lowered plan
// (ADR-20261102125208).
const (
	ManagedPostgresPlanExcessDatabaseLimit = "database_limit"
	ManagedPostgresPlanExcessServiceClass  = "service_class"
)

// managedPostgresServiceClasses are the classes a suspension can name.
var managedPostgresServiceClasses = map[string]bool{"development": true, "burstable": true, "production": true}

// ManagedPostgresPlanExcessNotice is the frozen snapshot one plan excess
// email reports.
type ManagedPostgresPlanExcessNotice struct {
	Reason       string
	DatabaseName string
	ServiceClass string
	// DeleteAt, when set, is when the database is deleted if it still does
	// not fit the plan (ADR-20261110184700).
	DeleteAt time.Time
}

// ManagedPostgresPlanExcessBodyV1 renders one plan excess email.
func ManagedPostgresPlanExcessBodyV1(email string, n ManagedPostgresPlanExcessNotice) (subject, body string, ok bool) {
	if !managedPostgresDatabaseName.MatchString(n.DatabaseName) || !managedPostgresServiceClasses[n.ServiceClass] {
		return "", "", false
	}
	var detail string
	switch n.Reason {
	case ManagedPostgresPlanExcessDatabaseLimit:
		detail = fmt.Sprintf(`Your plan now allows fewer databases than you have, so %s, one of your newest, is suspended.
To resume it, upgrade your plan or delete another database.`, n.DatabaseName)
	case ManagedPostgresPlanExcessServiceClass:
		detail = fmt.Sprintf(`Your plan no longer includes the %s class, so %s is suspended.
To resume it, upgrade your plan or resize it to a class your plan includes with gregale postgres resize.`, n.ServiceClass, n.DatabaseName)
	default:
		return "", "", false
	}
	kept := "Its data, credentials and app bindings are kept, but it does not start until then. It resumes within minutes once your account fits the plan."
	if !n.DeleteAt.IsZero() {
		kept = fmt.Sprintf(`Its data, credentials and app bindings are kept, but it does not start until then. It resumes within minutes once your account fits the plan.
If it still does not fit on %s, the database is deleted: its app bindings are removed, and you can recover it with gregale postgres recover for 7 days after that.`,
			n.DeleteAt.UTC().Format("2006-01-02 15:04 UTC"))
	}
	subject = fmt.Sprintf("Your database %s is suspended", n.DatabaseName)
	body = fmt.Sprintf(`Hi,

This is about a managed PostgreSQL database of your Gregale account (%s).
%s
%s

See your databases with gregale postgres list or on the PostgreSQL page of the dashboard.

— Gregale
`, safeRecipient(email), detail, kept)
	return subject, body, true
}
