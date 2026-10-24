// adr:20261019072523 — managed PostgreSQL credential receipts for candidate qualification.
package state

import (
	"context"
	"fmt"
	"time"
)

// EnvironmentQualificationPostgresCheck is one managed PostgreSQL credential
// delivery an enforced qualification candidate receives. The sealed material
// is private input for apid's verifier; it never enters receipts, reports or
// logs. Qualification candidates cannot reach their database, so apid checks
// the exact delivery from the control plane instead.
type EnvironmentQualificationPostgresCheck struct {
	RequestID            string
	Attempt              int64
	AccountID            string
	AppID                string
	Scope                string
	BindingID            string
	DatabaseID           string
	EnvironmentKey       string
	Access               string
	CredentialGeneration int64
	DeliveryVersion      int64
	PostgresMajor        int
	CredentialRef        string
	Ciphertext           []byte
	Kid                  string
	ValueHash            string
}

// EnvironmentQualificationPostgresStore lists unverified credential deliveries
// of a prepared graph's enforced candidates and records a receipt once apid
// has verified one. Recording fails closed when the lease, binding or delivery
// has changed since the check was listed.
type EnvironmentQualificationPostgresStore interface {
	PendingEnvironmentQualificationPostgresChecks(context.Context, string) ([]EnvironmentQualificationPostgresCheck, error)
	RecordEnvironmentQualificationPostgresReceipt(context.Context, EnvironmentQualificationPostgresCheck) error
}

func candidatePostgresAccessSupported(access string) bool {
	return access == "read_write" || access == "read_only" || access == "migration"
}

func qualificationPostgresReceiptKey(requestID string, attempt int64, bindingID string, generation, delivery int64) string {
	return fmt.Sprintf("%s/%s/%d/%d", qualificationSmokeReceiptKey(requestID, attempt), bindingID, generation, delivery)
}

// candidatePostgresDelivery is one managed binding's current credential
// delivery in the candidate's scope. Settled means ready with no rotation or
// cutover in progress.
type candidatePostgresDelivery struct {
	BindingID            string
	EnvironmentKey       string
	Access               string
	CredentialGeneration int64
	DeliveryVersion      int64
	Settled              bool
}

// candidatePostgresEvidenceDeadline mirrors environment_binding_postgres_deadline.
// It returns the zero time when the candidate has no PostgreSQL binding. A
// runtime credential must be the exact delivery the candidate was captured
// and restored with; migration credentials are release-only.
func candidatePostgresEvidenceDeadline(request EnvironmentWorkloadQualificationRequest, deliveries []candidatePostgresDelivery,
	receipts map[string]time.Time, captured, restored RuntimeConfigInputs, age time.Duration, now time.Time) (time.Time, error) {
	var until time.Time
	for _, d := range deliveries {
		if !candidatePostgresAccessSupported(d.Access) || !d.Settled || d.DeliveryVersion < 1 {
			return time.Time{}, ErrBindingReleaseRequired
		}
		recorded, ok := receipts[qualificationPostgresReceiptKey(request.ID, request.Attempt, d.BindingID, d.CredentialGeneration, d.DeliveryVersion)]
		if !ok || recorded.IsZero() || recorded.After(now) {
			return time.Time{}, ErrBindingReleaseRequired
		}
		if d.Access != "migration" {
			ref := request.FrozenInputs.Scope + "/" + d.EnvironmentKey
			if captured.SecretVersions[ref] != d.DeliveryVersion || restored.SecretVersions[ref] != d.DeliveryVersion {
				return time.Time{}, ErrBindingReleaseRequired
			}
		}
		if end := recorded.Add(age); until.IsZero() || end.Before(until) {
			until = end
		}
	}
	return until, nil
}
