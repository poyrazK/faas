package functionmigration

// adr: 1352

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sync"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"google.golang.org/protobuf/proto"
)

const (
	ReportVersion = 2
	ReportScope   = "native-migration-reply-boundaries-v1"
	Success       = "success"
	LostReadiness = "lost-readiness-reply"
	LostCommit    = "lost-commit-reply"
	Duplicate     = "duplicate-adoption"
)

func Scenarios() []string { return []string{Success, LostReadiness, LostCommit, Duplicate} }

// ErrReplyDiscarded models an error after the peer completed its effect. It is
// used only by the acceptance fixture, never by production retry policy.
var ErrReplyDiscarded = errors.New("acceptance fixture discarded a completed reply")

type Calls struct {
	PID         int    `json:"pid"`
	Checkpoints uint64 `json:"checkpoints"`
	Recoveries  uint64 `json:"recoveries"`
}

type Replays struct {
	RequestSHA string   `json:"request_sha256"`
	ReplySHA   string   `json:"reply_sha256"`
	Requests   []string `json:"replayed_request_sha256"`
	Replies    []string `json:"replayed_reply_sha256"`
}

type ReplyFault struct {
	Mode                  string    `json:"mode"`
	ReadinessDiscards     int       `json:"readiness_discards"`
	CommitDiscards        int       `json:"commit_discards"`
	RecoveredDecisions    int       `json:"recovered_decisions"`
	Replays               *Replays  `json:"replays,omitempty"`
	OwnerAfterReplays     Owner     `json:"owner_after_replays,omitzero"`
	SQLAfterReplays       []Session `json:"sql_after_replays,omitempty"`
	SourceKeptAfterReplay bool      `json:"source_kept_after_replay"`
	ClosedAfterReplay     bool      `json:"execution_closed_after_replay"`
}

// ReplayAdoption explicitly sends identical, independent protobuf requests.
// The caller, not the production scheduler, requests this test-only retry.
// Report only hashes: requests can contain sealed application inputs.
func ReplayAdoption(ctx context.Context, request *vmmdpb.AdoptMigratedInstanceRequest, reply *vmmdpb.AdoptMigratedInstanceResponse,
	call func(context.Context, *vmmdpb.AdoptMigratedInstanceRequest) (*vmmdpb.AdoptMigratedInstanceResponse, error), copies int) (Replays, error) {
	var evidence Replays
	if request == nil || reply == nil || call == nil || (copies != 1 && copies != 3) {
		return evidence, errors.New("invalid acceptance adoption retry")
	}
	var err error
	if evidence.RequestSHA, err = MessageSHA(request); err != nil {
		return evidence, err
	}
	if evidence.ReplySHA, err = MessageSHA(reply); err != nil {
		return evidence, err
	}
	evidence.Requests, evidence.Replies = make([]string, copies), make([]string, copies)
	errs := make([]error, copies)
	var group sync.WaitGroup
	for i := range copies {
		group.Go(func() {
			if errs[i] = ctx.Err(); errs[i] != nil {
				return
			}
			copy := proto.Clone(request).(*vmmdpb.AdoptMigratedInstanceRequest)
			evidence.Requests[i], errs[i] = MessageSHA(copy)
			if errs[i] != nil {
				return
			}
			var result *vmmdpb.AdoptMigratedInstanceResponse
			result, errs[i] = call(ctx, copy)
			if errs[i] != nil {
				return
			}
			if result == nil || !proto.Equal(result, reply) {
				errs[i] = errors.New("adoption retry changed the completed reply")
				return
			}
			evidence.Replies[i], errs[i] = MessageSHA(result)
		})
	}
	group.Wait()
	return evidence, errors.Join(errs...)
}

func MessageSHA(message proto.Message) (string, error) {
	data, err := (proto.MarshalOptions{Deterministic: true}).Marshal(message)
	if err != nil {
		return "", errors.New("cannot fingerprint acceptance RPC")
	}
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:]), nil
}
