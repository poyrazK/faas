package mail

import "fmt"

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
	subject = fmt.Sprintf("Your database %s is suspended", n.DatabaseName)
	body = fmt.Sprintf(`Hi,

This is about a managed PostgreSQL database of your Gregale account (%s).
%s
Its data, credentials and app bindings are kept, but it does not start until then. It resumes within minutes once your account fits the plan.

See your databases with gregale postgres list or on the PostgreSQL page of the dashboard.

— Gregale
`, safeRecipient(email), detail)
	return subject, body, true
}
