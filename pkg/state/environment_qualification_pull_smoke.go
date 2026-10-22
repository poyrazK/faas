// adr: 20261022155812
package state

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

// Pull smoke outcomes. A nack fails the candidate's smoke; it is never retried.
const (
	PullSmokeAcked  = "acked"
	PullSmokeNacked = "nacked"
)

// Pull smoke settle verbs, mirroring the production lease API.
const (
	PullSmokeAck    = "ack"
	PullSmokeNack   = "nack"
	PullSmokeExtend = "extend"
)

// pullSmokeMinVisibility mirrors the SDK consumers' smallest lease.
const pullSmokeMinVisibility = 2 * time.Second

var pullSmokeReceiptRE = regexp.MustCompile(`^[a-f0-9]{64}$`)

// EnvironmentQualificationPullSmoke is one reviewed pull-queue smoke message
// armed for the exact candidate execution that may lease it. The message never
// enters invocations or a customer queue. Queue name and payload come from the
// frozen review; the arming caller supplies only identity and the deadline.
type EnvironmentQualificationPullSmoke struct {
	RequestID  string
	Attempt    int64
	InstanceID string
	Binding    string
	QueueName  string
	MessageID  string
	Payload    json.RawMessage
	Deadline   time.Time
	Deliveries int
	Outcome    string
	SettledAt  *time.Time
	ArmedAt    time.Time
}

// EnvironmentQualificationPullCaller is the node-local network identity the
// qualification proxy observed (never a guest header), plus the app slug and
// queue the lease call names.
type EnvironmentQualificationPullCaller struct {
	NodeID string
	HostIP string
	App    string
	Queue  string
}

// EnvironmentQualificationPullLease is one leased smoke message and the
// receipt that owns it. Attempt counts deliveries of the smoke message.
type EnvironmentQualificationPullLease struct {
	MessageID      string
	Receipt        string
	Payload        json.RawMessage
	Attempt        int
	ArmedAt        time.Time
	LeaseExpiresAt time.Time
}

// EnvironmentQualificationPullSmokeStore lets schedd arm and observe a pull
// smoke and lets the node-local proxy lease and settle it for the one current
// candidate execution behind the observed node address.
type EnvironmentQualificationPullSmokeStore interface {
	ArmEnvironmentQualificationPullSmoke(ctx context.Context, smoke EnvironmentQualificationPullSmoke) error
	EnvironmentQualificationPullSmoke(ctx context.Context, requestID string, attempt int64, instanceID, binding string) (EnvironmentQualificationPullSmoke, error)
	LeaseEnvironmentQualificationPullSmoke(ctx context.Context, caller EnvironmentQualificationPullCaller, visibility time.Duration) ([]EnvironmentQualificationPullLease, error)
	SettleEnvironmentQualificationPullSmoke(ctx context.Context, caller EnvironmentQualificationPullCaller, verb string, receipts []string, visibility time.Duration) (QueueLeaseOutcome, error)
}

// qualificationPullSmokeFrozen derives the reviewed queue name and payload for
// one enabled pull binding of a worker candidate.
func qualificationPullSmokeFrozen(request EnvironmentWorkloadQualificationRequest, binding string) (string, json.RawMessage, error) {
	frozen, exists := request.FrozenInputs.QueueBindings[binding]
	if !exists || request.ExecutionMode != api.ExecutionModeWorker || frozen.Contract.Mode != "pull" ||
		frozen.Contract.Enabled == nil || !*frozen.Contract.Enabled || frozen.Contract.WorkloadClass != string(WorkloadClassWorker) ||
		!api.ValidQueueBindingName(frozen.Contract.QueueName) {
		return "", nil, ErrConflict
	}
	policy, _, err := EnvironmentQualificationSmokePolicyFor(request)
	if err != nil {
		return "", nil, err
	}
	for _, message := range policy.QueueMessages {
		if message.BindingName == binding && message.Mode == "pull" {
			return frozen.Contract.QueueName, append(json.RawMessage(nil), message.Payload...), nil
		}
	}
	return "", nil, ErrConflict
}

func qualificationPullSmokeArmValid(smoke EnvironmentQualificationPullSmoke) bool {
	_, messageErr := uuid.Parse(smoke.MessageID)
	return qualificationRecoveryUUIDValid(smoke.RequestID) && smoke.Attempt > 0 && qualificationRecoveryUUIDValid(smoke.InstanceID) &&
		api.ValidQueueBindingName(smoke.Binding) && messageErr == nil && !smoke.Deadline.IsZero()
}

func qualificationPullCallerValid(caller EnvironmentQualificationPullCaller) bool {
	return qualificationNetworkIdentityValid(caller.NodeID, caller.HostIP) && api.ValidAppSlug(caller.App) && api.ValidQueueBindingName(caller.Queue)
}

// qualificationPullVisibility bounds a lease to the same window production
// consumers use and never past the smoke's own deadline.
func qualificationPullVisibility(visibility time.Duration, now, deadline time.Time) (time.Time, bool) {
	if visibility < pullSmokeMinVisibility {
		return time.Time{}, false
	}
	until := now.Add(visibility)
	if until.After(deadline) {
		until = deadline
	}
	return until, now.Before(until)
}

func newQualificationPullReceipt() (string, string, error) {
	var token [32]byte
	if _, err := rand.Read(token[:]); err != nil {
		return "", "", err
	}
	receipt := hex.EncodeToString(token[:])
	return receipt, qualificationPullReceiptSHA256(receipt), nil
}

func qualificationPullReceiptSHA256(receipt string) string {
	sum := sha256.Sum256([]byte(receipt))
	return hex.EncodeToString(sum[:])
}

func qualificationPullReceiptValid(receipt string) bool {
	return pullSmokeReceiptRE.MatchString(receipt)
}

func qualificationPullSettleValid(verb string, receipts []string) bool {
	if verb != PullSmokeAck && verb != PullSmokeNack && verb != PullSmokeExtend || len(receipts) == 0 || len(receipts) > api.QueueLeaseMaxReceipts {
		return false
	}
	return true
}
