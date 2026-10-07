package state

// adr: 435

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

func lockStandardNativeArtifactInputs(ctx context.Context, tx pgx.Tx, input *nativeBootLockedInputs, id string, historicalRetry bool) error {
	capture, err := decodeInstanceStandardAdmission(id, input.Snapshot, time.Unix(0, input.ClockUnixNano))
	if err != nil {
		return err
	}
	capture.NodeID, capture.NativeInputHash = input.NodeID, input.CapturedInputHash
	input.capture = capture
	if historicalRetry {
		return nil // Reading a committed acknowledgment does not issue authority.
	}
	evidence := DeploymentRuntimeScanEvidence{}
	if capture.ArtifactInputHash != "" {
		evidence, err = freshRuntimeScanTx(ctx, tx, capture.AccountID, capture.AppID, capture.DeploymentID)
		if err != nil {
			return err
		}
		input.ClockUnixNano = evidence.CheckedAt.UnixNano()
	}
	deadline, err := standardNativeArtifactDeadline(capture, evidence)
	if err != nil {
		return err
	}
	if !deadline.IsZero() && (input.ArtifactExpiresAtUnixNano == 0 || deadline.UnixNano() < input.ArtifactExpiresAtUnixNano) {
		input.ArtifactExpiresAtUnixNano = deadline.UnixNano()
	}
	return nil
}

func (input nativeBootLockedInputs) artifactDeadline() time.Time {
	if input.ArtifactExpiresAtUnixNano == 0 {
		return time.Time{}
	}
	return time.Unix(0, input.ArtifactExpiresAtUnixNano).UTC()
}
