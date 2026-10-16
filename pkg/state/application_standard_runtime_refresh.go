// adr: 595 — installing a revision durably hands runtime convergence to schedd.
package state

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/google/uuid"
)

var ErrApplicationStandardRefreshDeferred = errors.New("state: application standard runtime refresh deferred")

// This is a private scheduler handoff, not an application API or observation.
type ApplicationStandardRuntimeRefresh struct {
	OrgID           string `json:"org_id"`
	DesiredRevision int64  `json:"desired_revision"`
	EffectiveHash   string `json:"effective_hash"`
}

type ApplicationStandardRuntimeRefreshRequest struct {
	AppID    string                            `json:"app_id"`
	WakeID   string                            `json:"wake_id"`
	Standard ApplicationStandardRuntimeRefresh `json:"application_standard"`
}

type ApplicationStandardRuntimeRefreshStore interface {
	GetApplicationStandardRuntimeRefresh(context.Context, string, string) (ApplicationStandardRuntimeRefreshRequest, error)
	CheckApplicationStandardRuntimeRefresh(context.Context, ApplicationStandardRuntimeRefreshRequest) (bool, error)
}

func newStandardRuntimeRefresh(e ApplicationStandardEnrollment) ApplicationStandardRuntimeRefreshRequest {
	appID := canonicalStandardUUID(e.AppID)
	wake := uuid.NewSHA1(uuid.NameSpaceOID, []byte(fmt.Sprintf("gregale:application-standard:%s:%d:%s", appID, e.DesiredRevision, e.EffectiveHash)))
	return ApplicationStandardRuntimeRefreshRequest{AppID: appID, WakeID: wake.String(), Standard: ApplicationStandardRuntimeRefresh{OrgID: canonicalStandardUUID(e.OrgID), DesiredRevision: e.DesiredRevision, EffectiveHash: e.EffectiveHash}}
}

func validStandardRuntimeRefresh(r ApplicationStandardRuntimeRefreshRequest) bool {
	hash, err := hex.DecodeString(r.Standard.EffectiveHash)
	return validStandardResourceRead(r.Standard.OrgID, r.AppID) && validStandardResourceRead(r.Standard.OrgID, r.WakeID) && r.Standard.DesiredRevision > 0 && err == nil && len(hash) == 32
}

func standardRuntimeRefreshCurrent(r ApplicationStandardRuntimeRefreshRequest, e ApplicationStandardEnrollment, paused bool) (bool, error) {
	if !sameStandardUUID(r.Standard.OrgID, e.OrgID) || !sameStandardUUID(r.AppID, e.AppID) {
		return false, ErrNotFound
	}
	if e.DesiredRevision > r.Standard.DesiredRevision {
		return false, nil // A newer durable handoff owns convergence.
	}
	if e.DesiredRevision != r.Standard.DesiredRevision || e.EffectiveHash != r.Standard.EffectiveHash {
		return false, ErrConflict
	}
	if e.PersistedRevision != e.DesiredRevision || paused {
		return false, ErrApplicationStandardRefreshDeferred
	}
	return true, nil
}
