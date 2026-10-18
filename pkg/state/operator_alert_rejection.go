package state

import (
	"encoding/json"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func encodeOperatorAlertRejection(at time.Time, d api.ObsAlertCollectionRejection) ([]byte, error) {
	at = at.UTC().Truncate(time.Microsecond)
	if !api.ValidObsAlertCollectionRejection(at, d) {
		return nil, ErrInvalidArgument
	}
	return json.Marshal(d)
}

func decodeOperatorAlertRejection(at time.Time, data []byte) (*api.ObsAlertCollectionRejection, error) {
	at = at.UTC().Truncate(time.Microsecond)
	if data == nil {
		return nil, nil
	}
	var out api.ObsAlertCollectionRejection
	if len(data) > api.ObsAlertCollectionRejectionStorageMaxBytes || json.Unmarshal(data, &out) != nil || !api.ValidObsAlertCollectionRejection(at, out) {
		return nil, ErrInvalidArgument
	}
	return &out, nil
}

func collectionFailureDetail(at time.Time, reason string, d *api.ObsAlertCollectionRejection) api.ObsAlertCollectionFailure {
	out := api.ObsAlertCollectionFailure{AttemptedAt: at, Reason: reason, DiagnosticsState: "unknown", Diagnostics: d}
	if d != nil {
		out.DiagnosticsState = "recorded"
	}
	return out
}
