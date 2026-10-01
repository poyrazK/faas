package state

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/runtimeadmission"
)

// Boot grants/receipts are private scheduler authority. They never acknowledge
// image content, delivered logs or live-egress convergence.
type InstanceApplicationStandardBootStore interface {
	IssueInstanceApplicationStandardBoot(context.Context, string, runtimeadmission.Binding) (runtimeadmission.Binding, error)
	PublishInstanceApplicationStandardRuntime(context.Context, string, State, runtimeadmission.Receipt) (Instance, error)
}

type ComputeNodeRuntimeIdentityStore interface {
	RegisterComputeNodeRuntimeIdentity(context.Context, runtimeadmission.Identity) error
}

type instanceStandardBoot struct {
	Binding       runtimeadmission.Binding
	ExpectedState string
	Receipt       *runtimeadmission.Receipt
	ReceivedAt    time.Time
}

type nativeBootLockedInputs struct {
	Snapshot          json.RawMessage `json:"input_snapshot"`
	CapturedInputHash string          `json:"captured_input_hash"`
	NodeID            string          `json:"node_id"`
	Incarnation       string          `json:"incarnation"`
	ClockUnixNano     int64           `json:"clock_unix_nano"`
}

func validateStandardBootBinding(binding runtimeadmission.Binding, capture InstanceApplicationStandardAdmission, incarnation string, now time.Time) error {
	if err := binding.Validate(now); err != nil {
		if errors.Is(err, runtimeadmission.ErrExpired) {
			return ErrApplicationStandardRuntimeStale
		}
		return ErrInvalidArgument
	}
	if !capture.Managed || binding.InstanceID != capture.InstanceID || binding.AppID != capture.AppID || binding.AccountID != capture.AccountID || binding.DeploymentID != capture.DeploymentID || binding.NodeID != capture.NodeID || binding.Incarnation != incarnation || binding.DesiredRevision != capture.DesiredRevision || capture.PersistedRevision != capture.DesiredRevision || binding.EffectiveHash != capture.EffectiveHash || binding.CapturedInputHash != capture.NativeInputHash || binding.EgressRevision != capture.EgressRevision {
		return ErrApplicationStandardRuntimeStale
	}
	return nil
}

func standardRuntimeReceiptTarget(next State, receipt runtimeadmission.Receipt) bool {
	return next == StateRunning && !receipt.Paused || next == StateWarm && receipt.Paused
}
