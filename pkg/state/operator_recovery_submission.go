// adr: 1524
package state

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

// OperatorRecoverySubmissionStore accepts a recovery action and its request
// receipt/audit atomically. It must never fall back to InsertOperatorIntent.
type OperatorRecoverySubmissionStore interface {
	SubmitOperatorRecovery(context.Context, OperatorRecoverySubmission) (OperatorRecoveryReceipt, error)
}

type OperatorRecoverySubmission struct {
	ActorID    string
	RequestKey string
	Kind       OperatorIntentKind
	Target     string // Requested instance UUID or app slug, not the resolved deployment.
	Reason     string
	TraceID    *string
}

type OperatorRecoveryReceipt struct {
	IntentID      string
	TargetID      string
	Kind          OperatorIntentKind
	ResponseBody  []byte // Exact original 202 representation, including its polling horizon.
	AuditOutboxID int64
	Replayed      bool
}

var (
	ErrOperatorRecoveryKeyConflict = fmt.Errorf("recovery key belongs to different parameters: %w", ErrConflict)
	ErrOperatorRecoveryLegacyKey   = fmt.Errorf("legacy recovery submission requires reconciliation: %w", ErrConflict)
	ErrOperatorRecoveryDeployment  = fmt.Errorf("app has no deployment: %w", ErrNotFound)
	operatorRecoveryReasonShape    = regexp.MustCompile(`^[a-z0-9_]+$`)
)

type OperatorRecoveryIneligibleError struct {
	InstanceID, AppID, DeploymentID, AccountID, State string
}

func (e *OperatorRecoveryIneligibleError) Error() string { return "current state: " + e.State }

func ValidOperatorRecoveryKey(key string) bool {
	if key == "" || len(key) > api.OperatorRecoveryRequestKeyMaxBytes {
		return false
	}
	for _, b := range []byte(key) {
		if b < '!' || b > '~' {
			return false
		}
	}
	return true
}

// NormalizeOperatorRecovery validates immutable request parameters before any
// receipt lookup. Trace attribution is deliberately excluded from the digest.
func NormalizeOperatorRecovery(in OperatorRecoverySubmission) (OperatorRecoverySubmission, error) {
	actor, err := uuid.Parse(in.ActorID)
	if err != nil || actor == uuid.Nil || !ValidOperatorRecoveryKey(in.RequestKey) {
		return in, ErrInvalidArgument
	}
	in.ActorID = actor.String()
	switch in.Kind {
	case OperatorIntentKindForcePark, OperatorIntentKindForceRestart:
		id, parseErr := uuid.Parse(in.Target)
		if parseErr != nil || id == uuid.Nil {
			return in, ErrInvalidArgument
		}
		in.Target = id.String()
	case OperatorIntentKindForceColdBoot:
		if in.Target == "" || len(in.Target) > api.OperatorRecoveryTargetMaxBytes {
			return in, ErrInvalidArgument
		}
	default:
		return in, ErrInvalidArgument
	}
	if in.Reason == "" {
		in.Reason = "operator_" + string(in.Kind)
	}
	if len(in.Reason) > api.OperatorRecoveryReasonMaxBytes || !operatorRecoveryReasonShape.MatchString(in.Reason) {
		return in, ErrInvalidArgument
	}
	if in.TraceID != nil && !isOTelHex32(*in.TraceID) {
		return in, ErrInvalidArgument
	}
	return in, nil
}

func operatorRecoveryDigests(in OperatorRecoverySubmission) (string, string) {
	key := sha256.Sum256([]byte(in.RequestKey))
	// JSON encodes boundaries unambiguously; version changes require an explicit migration.
	encoded, _ := json.Marshal([]string{"operator-recovery-v1", string(in.Kind), in.Target, in.Reason})
	request := sha256.Sum256(encoded)
	return hex.EncodeToString(key[:]), hex.EncodeToString(request[:])
}

type operatorRecoveryTarget struct {
	InstanceID, AppID, DeploymentID, AccountID, State string
}

func operatorRecoveryEligible(in OperatorRecoverySubmission, target operatorRecoveryTarget) error {
	if in.Kind == OperatorIntentKindForceColdBoot || target.State == string(StateRunning) ||
		in.Kind == OperatorIntentKindForcePark && (target.State == string(StateWaking) || target.State == string(StateColdBooting)) {
		return nil
	}
	return &OperatorRecoveryIneligibleError{target.InstanceID, target.AppID, target.DeploymentID, target.AccountID, target.State}
}

func OperatorRecoveryAuditKind(kind OperatorIntentKind) string {
	switch kind {
	case OperatorIntentKindForcePark:
		return "operator.action.park_instance"
	case OperatorIntentKindForceRestart:
		return "operator.action.restart_instance"
	default:
		return "operator.action.force_cold_boot"
	}
}

func newOperatorRecoveryReceipt(in OperatorRecoverySubmission, target operatorRecoveryTarget, now time.Time) (OperatorRecoveryReceipt, []byte, error) {
	id := uuid.NewString()
	response := api.OperatorIntentAcceptedResponse{OK: true, IntentID: id, StatusURL: "/v1/admin/operator-intents/" + id,
		ExpiresAt: now.Add(api.OperatorRecoveryPollHorizon), Kind: string(in.Kind), Reason: in.Reason}
	targetID := target.InstanceID
	data := map[string]any{"actor": in.ActorID, "target_account_id": target.AccountID, "intent_id": id,
		"app_id": target.AppID, "deployment_id": target.DeploymentID, "reason": in.Reason, "result": "enqueued"}
	if in.TraceID != nil {
		data["trace_id"] = *in.TraceID
	}
	if in.Kind == OperatorIntentKindForceColdBoot {
		targetID = target.DeploymentID
		response.AppID, response.DeploymentID = target.AppID, target.DeploymentID
	} else {
		response.InstanceID, response.PreviousState = target.InstanceID, target.State
		data["instance_id"], data["previous_state"] = target.InstanceID, target.State
	}
	body, err := json.Marshal(response)
	if err != nil {
		return OperatorRecoveryReceipt{}, nil, err
	}
	audit, err := json.Marshal(data)
	return OperatorRecoveryReceipt{IntentID: id, TargetID: targetID, Kind: in.Kind, ResponseBody: append(body, '\n')}, audit, err
}

func replayOperatorRecovery(receipt OperatorRecoveryReceipt, expected, actual string) (OperatorRecoveryReceipt, error) {
	if expected != actual {
		return OperatorRecoveryReceipt{}, ErrOperatorRecoveryKeyConflict
	}
	if receipt.IntentID == "" || len(receipt.ResponseBody) == 0 || receipt.AuditOutboxID <= 0 {
		return OperatorRecoveryReceipt{}, errors.New("incomplete operator recovery receipt")
	}
	receipt.ResponseBody = append([]byte(nil), receipt.ResponseBody...)
	receipt.Replayed = true
	return receipt, nil
}
