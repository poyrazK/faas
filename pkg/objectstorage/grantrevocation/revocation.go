// Package grantrevocation defines private native-write capability retirement.
// Its observations do not establish complete writer coverage or a capture point.
package grantrevocation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"github.com/google/uuid"
)

var ErrInvalid = errors.New("invalid native object grant revocation")

// Scope pins the frozen source and owner. It contains no provider credentials
// or native URLs and is never returned through a customer API.
type Scope struct {
	OperationID, AccountID, ProjectID, BucketID, AppID string
	SourceRevisionHash, SourceScope                    string
	BackendID, BackendFingerprint, PhysicalName        string
}

// Plan is retained before dispatch. GrantIDs are the original tracked native
// receipts, sorted and unique; retries must not recapture a newer selection.
type Plan struct {
	Scope     Scope
	RequestID string
	GrantIDs  []string
}

func canonicalID(id string) bool {
	parsed, err := uuid.Parse(id)
	return err == nil && parsed != uuid.Nil && parsed.String() == id
}

func (p Plan) Validate() error {
	for _, id := range []string{p.Scope.OperationID, p.Scope.AccountID, p.Scope.ProjectID, p.Scope.BucketID, p.Scope.AppID, p.RequestID} {
		if !canonicalID(id) {
			return ErrInvalid
		}
	}
	for _, hash := range []string{p.Scope.SourceRevisionHash, p.Scope.BackendFingerprint} {
		decoded, err := hex.DecodeString(hash)
		if err != nil || len(decoded) != sha256.Size || strings.ToLower(hash) != hash {
			return ErrInvalid
		}
	}
	if p.Scope.SourceScope == "" || p.Scope.BackendID == "" || p.Scope.PhysicalName == "" || len(p.GrantIDs) == 0 {
		return ErrInvalid
	}
	for i, id := range p.GrantIDs {
		if !canonicalID(id) || i > 0 && p.GrantIDs[i-1] >= id {
			return ErrInvalid
		}
	}
	return nil
}

func (p Plan) SHA256() (string, error) {
	if err := p.Validate(); err != nil {
		return "", err
	}
	data, err := json.Marshal(p)
	if err != nil {
		return "", ErrInvalid
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func (p Plan) Clone() Plan {
	p.GrantIDs = slices.Clone(p.GrantIDs)
	return p
}

// Observation comes from a separate authenticated read of the provider's
// immutable revocation journal, including already admitted native writes.
// RevocationID must identify the same permanent retirement on every retry.
type Observation struct {
	Scope                  Scope
	RequestID, PlanSHA256  string
	RevocationID           string
	AllNativeGrantsRevoked bool
	InFlightWrites         int64
}

func (o Observation) Validate(p Plan) error {
	hash, err := p.SHA256()
	if err != nil || o.Scope != p.Scope || o.RequestID != p.RequestID || o.PlanSHA256 != hash ||
		o.RevocationID == "" || len(o.RevocationID) > 512 || strings.ContainsAny(o.RevocationID, "\r\n\x00") || o.InFlightWrites < 0 {
		return ErrInvalid
	}
	return nil
}

func (o Observation) Drained() bool {
	return o.AllNativeGrantsRevoked && o.InFlightWrites == 0
}

// Provider is optional. Implementations must authenticate the exact source and
// original capability selection, durably bind RequestID to Plan.SHA256, and
// resume that same permanent retirement after reply loss or credential rotation.
// They must reject unsupported or unknown issuance identities, never rotate a
// shared credential affecting unrelated buckets, and never revoke newer grants.
// No temporary provider admission hold may be created by this protocol.
// Credentials and native URLs must never appear in results or errors.
// A signing reply, URL expiry, missing journal or elapsed time is not evidence.
// Built-in S3/GCS providers deliberately do not implement this capability.
type Provider interface {
	RevokeNativeWriteGrants(context.Context, Plan) error
	ObserveNativeWriteGrantRevocation(context.Context, Plan) (Observation, error)
}
