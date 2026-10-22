// adr:20261019121546 — managed object storage credential receipts for candidate qualification.
package state

import (
	"context"
	"maps"
	"time"

	"github.com/google/uuid"
)

// EnvironmentQualificationObjectStorageSecret is one of a managed S3
// credential's six sealed deliveries. Ciphertext is private verifier input.
type EnvironmentQualificationObjectStorageSecret struct {
	Key             string `json:"key"`
	DeliveryVersion int64  `json:"delivery_version"`
	Ciphertext      []byte `json:"ciphertext"`
	Kid             string `json:"kid"`
	ValueHash       string `json:"value_hash"`
}

// EnvironmentQualificationObjectStorageCheck is one active managed S3
// credential an enforced qualification candidate receives. Mode is the probe
// the policy requirement and permission select.
type EnvironmentQualificationObjectStorageCheck struct {
	RequestID    string
	Attempt      int64
	AccountID    string
	AppID        string
	Scope        string
	CredentialID string
	Prefix       string
	Permission   string
	Mode         string
	Secrets      []EnvironmentQualificationObjectStorageSecret
}

// Deliveries returns the scoped delivery versions the receipt pins.
func (c EnvironmentQualificationObjectStorageCheck) Deliveries() map[string]int64 {
	deliveries := make(map[string]int64, len(c.Secrets))
	for _, s := range c.Secrets {
		deliveries[c.Scope+"/"+s.Key] = s.DeliveryVersion
	}
	return deliveries
}

// EnvironmentQualificationObjectStorageStore lists unverified managed S3
// credentials of a prepared graph's enforced candidates and records a receipt
// once apid has verified one. Recording fails closed on any change.
type EnvironmentQualificationObjectStorageStore interface {
	PendingEnvironmentQualificationObjectStorageChecks(context.Context, string) ([]EnvironmentQualificationObjectStorageCheck, error)
	RecordEnvironmentQualificationObjectStorageReceipt(context.Context, EnvironmentQualificationObjectStorageCheck) error
}

func objectStorageModeCovers(have, need string) bool {
	return need == "" || have == need || have == "read_write"
}

// candidateObjectStorageProbeMode follows the policy requirement; without one
// a write-only credential cannot list, so it uses the write canary.
func candidateObjectStorageProbeMode(required, permission string) string {
	if required != "" {
		return required
	}
	if permission == "write" {
		return "write"
	}
	return "read"
}

func validObjectStorageQualificationCheck(c EnvironmentQualificationObjectStorageCheck) bool {
	_, requestErr := uuid.Parse(c.RequestID)
	_, credentialErr := uuid.Parse(c.CredentialID)
	if requestErr != nil || credentialErr != nil || c.Attempt < 1 || c.Prefix == "" || len(c.Secrets) != 6 ||
		!objectStorageModeCovers(c.Permission, c.Mode) || c.Mode != "read" && c.Mode != "write" && c.Mode != "read_write" {
		return false
	}
	for _, s := range c.Secrets {
		if s.Key == "" || s.DeliveryVersion < 1 {
			return false
		}
	}
	return true
}

type candidateObjectStorageReceipt struct {
	Prefix     string
	Permission string
	Mode       string
	Deliveries map[string]int64
	RecordedAt time.Time
}

func qualificationObjectStorageReceiptKey(requestID string, attempt int64, credentialID string) string {
	return qualificationSmokeReceiptKey(requestID, attempt) + "/" + credentialID
}

// candidateObjectStorageDelivery is one active managed S3 credential in the
// candidate scope with its current scoped delivery versions.
type candidateObjectStorageDelivery struct {
	CredentialID string
	Prefix       string
	Permission   string
	Deliveries   map[string]int64
	Rotating     bool
}

// candidateObjectStorageEvidenceDeadline mirrors
// environment_binding_object_storage_deadline. It returns the zero time when
// the candidate has no object storage binding.
func candidateObjectStorageEvidenceDeadline(request EnvironmentWorkloadQualificationRequest, deliveries []candidateObjectStorageDelivery,
	requirements map[string]string, receipts map[string]candidateObjectStorageReceipt, captured, restored RuntimeConfigInputs, age time.Duration, now time.Time) (time.Time, error) {
	bound := map[string]bool{}
	for _, d := range deliveries {
		bound[d.Prefix] = true
	}
	for prefix := range requirements {
		if !bound[prefix] {
			return time.Time{}, ErrBindingReleaseRequired
		}
	}
	var until time.Time
	for _, d := range deliveries {
		required := requirements[d.Prefix]
		r, ok := receipts[qualificationObjectStorageReceiptKey(request.ID, request.Attempt, d.CredentialID)]
		if d.Rotating || len(d.Deliveries) != 6 || !ok || r.RecordedAt.IsZero() || r.RecordedAt.After(now) || r.Prefix != d.Prefix ||
			r.Permission != d.Permission || !maps.Equal(r.Deliveries, d.Deliveries) || !objectStorageModeCovers(r.Mode, required) ||
			!objectStorageModeCovers(d.Permission, required) {
			return time.Time{}, ErrBindingReleaseRequired
		}
		for ref, version := range d.Deliveries {
			if captured.SecretVersions[ref] != version || restored.SecretVersions[ref] != version {
				return time.Time{}, ErrBindingReleaseRequired
			}
		}
		if end := r.RecordedAt.Add(age); until.IsZero() || end.Before(until) {
			until = end
		}
	}
	return until, nil
}
