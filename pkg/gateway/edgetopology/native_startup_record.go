package edgetopology

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"slices"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway/ingress"
)

// ValidateNativeStartupRecord checks only canonical storage shape and selected
// identity consistency. It does NOT authenticate HMAC, freshness, live processes,
// physical resources or fencing. It cannot construct an opaque observation.
func ValidateNativeStartupRecord(raw []byte, review NativeStartupReview) error {
	frozen, expected, err := CanonicalNativeStartupReview(review)
	if err != nil || len(raw) == 0 || len(raw) > api.RuntimeUpgradeNativeStartupEvidenceMaxBytes {
		return ErrNativeUnverified
	}
	var snapshot NativeStartupSnapshot
	if json.Unmarshal(raw, &snapshot) != nil || !reflect.DeepEqual(snapshot.Review, frozen) || len(snapshot.Native) != 2 || snapshot.CheckedAt.IsZero() {
		return ErrNativeUnverified
	}
	canonical, err := json.Marshal(snapshot)
	if err != nil || !bytes.Equal(raw, canonical) || !sameNativeScope(snapshot.Native[0], snapshot.Native[1]) {
		return ErrNativeUnverified
	}
	for _, native := range snapshot.Native {
		if !validNativeStartupSnapshot(native, frozen.Scope) {
			return ErrNativeUnverified
		}
	}
	if snapshot.Native[1].CheckedAt.Before(snapshot.Native[0].CheckedAt) || snapshot.CheckedAt.Before(snapshot.Native[1].CheckedAt) || !nativeStoredStartupProof(snapshot.Proof, expected) {
		return ErrNativeUnverified
	}
	return nil
}

func validNativeStartupSnapshot(got NativeScopeObservation, scope NativeScopeReview) bool {
	if got.CheckedAt.IsZero() || !reflect.DeepEqual(got.Host, scope.Host) || len(got.Services) != 1 {
		return false
	}
	s := got.Services[0]
	want := scope.Services[0]
	if !reflect.DeepEqual(s.Review, want) || s.Executable.Inode == 0 || s.Executable.SHA256 != want.ExeSHA256 || s.Cgroup.Inode != want.CgroupInode || s.Cgroup.SHA256 != "" || len(s.Listeners) != len(want.TCPListeners) {
		return false
	}
	inodes, fds := make(map[uint64]bool), make(map[int]bool)
	for i, listener := range s.Listeners {
		if listener.Address != want.TCPListeners[i] || listener.Inode == 0 || inodes[listener.Inode] || len(listener.FDs) == 0 || !slices.IsSorted(listener.FDs) {
			return false
		}
		inodes[listener.Inode] = true
		for _, fd := range listener.FDs {
			if fd < 0 || uint64(fd) > uint64(1<<31-1) || fds[fd] || len(fds) >= api.RuntimeUpgradeNativeFDLimit {
				return false
			}
			fds[fd] = true
		}
	}
	return true
}

func nativeStoredStartupProof(raw []byte, expected ingress.NativePublicStartup) bool {
	if len(raw) < 1 || len(raw) > api.RuntimeUpgradeIngressIdentityMaxBytes {
		return false
	}
	var envelope struct {
		Startup ingress.NativePublicStartup `json:"startup"`
		Nonce   string                      `json:"nonce"`
		Proof   string                      `json:"proof"`
	}
	if json.Unmarshal(raw, &envelope) != nil || envelope.Startup != expected {
		return false
	}
	nonce, err := uuid.Parse(envelope.Nonce)
	if err != nil || nonce == uuid.Nil || nonce.String() != envelope.Nonce {
		return false
	}
	proof, err := hex.DecodeString(envelope.Proof)
	if err != nil || len(proof) != 32 || hex.EncodeToString(proof) != envelope.Proof {
		return false
	}
	canonical, err := json.Marshal(envelope)
	return err == nil && bytes.Equal(canonical, raw)
}
