package state

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

var ErrApplicationStandardLogDeliveryStale = errors.New("state: application standard logging projection changed")

// Private consumer metadata accompanies the concrete configuration loaded by
// gatewayd. It is never serialized in API responses or restoration backups.
type ApplicationStandardLogDrainBinding struct {
	OrgID              string `json:"org_id"`
	AppID              string `json:"app_id"`
	DrainID            string `json:"drain_id"`
	ResourceID         string `json:"resource_id"`
	DesiredRevision    int64  `json:"desired_revision"`
	EffectiveHash      string `json:"effective_hash"`
	ResourceConfigHash string `json:"resource_config_hash"`
	DrainConfigHash    string `json:"drain_config_hash"`
}

type ApplicationStandardLogDeliveryObservation struct {
	ApplicationStandardLogDrainBinding
	SourceInstanceID string    `json:"source_instance_id"`
	Sequence         int64     `json:"sequence"`
	ObservedAt       time.Time `json:"observed_at"`
}

type ApplicationStandardLogDeliveryStore interface {
	RecordApplicationStandardLogDelivery(context.Context, AppLogDrain, string, uint64) (ApplicationStandardLogDeliveryObservation, error)
	ListApplicationStandardLogDeliveries(context.Context, string, string) ([]ApplicationStandardLogDeliveryObservation, error)
}

// Fingerprint the exact sender tuple, including ciphertext rather than plaintext.
// The final ciphertext component is binary and has no delimiter ambiguity.
func ApplicationStandardLogDrainConfigHash(d AppLogDrain) string {
	parts := []string{"gregale.standard.log-drain.v1", d.ID, d.AppID, d.AccountID}
	for i := 1; i < len(parts); i++ {
		id, err := uuid.Parse(parts[i])
		if err != nil || id == uuid.Nil {
			return ""
		}
		parts[i] = id.String()
	}
	parts = append(parts, string(d.Kind), d.TargetURL, strconv.FormatBool(d.Enabled))
	body := append([]byte(strings.Join(parts, "\x00")+"\x00"), d.AuthHeaderSealed...)
	hash := sha256.Sum256(body)
	return hex.EncodeToString(hash[:])
}

func validStandardLogDelivery(d AppLogDrain, source string, seq uint64) bool {
	b := d.StandardBinding
	if b == nil || seq == 0 || seq > uint64(api.ApplicationStandardMaxLogSequence) || !d.Enabled || b.DesiredRevision <= 0 || b.DesiredRevision > api.ApplicationStandardMaxVersion {
		return false
	}
	for _, raw := range []string{b.OrgID, b.AppID, b.DrainID, b.ResourceID, source} {
		id, err := uuid.Parse(raw)
		if err != nil || id == uuid.Nil {
			return false
		}
	}
	return sameStandardUUID(b.AppID, d.AppID) && sameStandardUUID(b.DrainID, d.ID) && b.DrainConfigHash == ApplicationStandardLogDrainConfigHash(d) && len(b.EffectiveHash) == sha256.Size*2 && len(b.ResourceConfigHash) == sha256.Size*2
}

func sameStandardLogBinding(a, b *ApplicationStandardLogDrainBinding) bool {
	return a == nil && b == nil || a != nil && b != nil && *a == *b
}

func standardLogDeliveryKey(b ApplicationStandardLogDrainBinding) string {
	return standardBindingKey(b.AppID, "log_destinations", b.ResourceID)
}

func standardLogDeliveryObservation(d AppLogDrain, source string, seq uint64, now time.Time) ApplicationStandardLogDeliveryObservation {
	return ApplicationStandardLogDeliveryObservation{ApplicationStandardLogDrainBinding: *d.StandardBinding, SourceInstanceID: canonicalStandardUUID(source), Sequence: int64(seq), ObservedAt: now.UTC().Truncate(time.Microsecond)}
}

func standardLogBindingFromCurrent(d AppLogDrain, e ApplicationStandardEnrollment, b standardControlBinding, r ApplicationStandardLogDestination) *ApplicationStandardLogDrainBinding {
	ids := append(standardReviewStrings(e.Effective.Values["log_destinations"]), e.AdditionalLogDestinations...)
	if !slices.Contains(ids, canonicalStandardUUID(b.ResourceID)) || r.ConfigHash == "" || !sameStandardUUID(r.OrgID, e.OrgID) {
		return nil
	}
	return &ApplicationStandardLogDrainBinding{OrgID: canonicalStandardUUID(e.OrgID), AppID: canonicalStandardUUID(e.AppID), DrainID: canonicalStandardUUID(d.ID), ResourceID: canonicalStandardUUID(r.ID), DesiredRevision: e.DesiredRevision, EffectiveHash: e.EffectiveHash, ResourceConfigHash: r.ConfigHash, DrainConfigHash: ApplicationStandardLogDrainConfigHash(d)}
}

func standardLogDeliveryError(err error) error {
	if errors.Is(err, ErrNotFound) {
		return ErrApplicationStandardLogDeliveryStale
	}
	return fmt.Errorf("application standard log delivery: %w", err)
}
