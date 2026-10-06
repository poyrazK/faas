package sched

import (
	"crypto/ecdsa"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"sort"
	"time"
)

// NodeInstanceInventory is an explicit, authenticated vmmd process inventory.
// Metrics are never an inventory: a failed cgroup read must not fail live VMs.
type NodeInstanceInventory struct {
	NodeID      string
	NodeKeyID   string
	SampledAt   time.Time
	Complete    bool
	InstanceIDs []string
	Signature   []byte
}

func (r NodeInstanceInventory) digest() []byte {
	buf := []byte("faas.instance-inventory.v1")
	appendString := func(s string) {
		buf = binary.BigEndian.AppendUint32(buf, uint32(len(s)))
		buf = append(buf, s...)
	}
	appendString(r.NodeID)
	appendString(r.NodeKeyID)
	buf = binary.BigEndian.AppendUint64(buf, uint64(r.SampledAt.UnixMilli()))
	if r.Complete {
		buf = append(buf, 1)
	} else {
		buf = append(buf, 0)
	}
	ids := append([]string(nil), r.InstanceIDs...)
	sort.Strings(ids)
	buf = binary.BigEndian.AppendUint32(buf, uint32(len(ids)))
	for _, id := range ids {
		appendString(id)
	}
	digest := sha256.Sum256(buf)
	return digest[:]
}

func SignNodeInventory(key *ecdsa.PrivateKey, r NodeInstanceInventory) ([]byte, error) {
	if key == nil || key.Curve != ecdsaP256() || r.SampledAt.UnixMilli() < 0 {
		return nil, errors.New("sched: inventory signing requires P-256 and a valid timestamp")
	}
	rInt, sInt, err := ecdsaSignDeterministic(key, r.digest())
	if err != nil {
		return nil, err
	}
	sig := make([]byte, 64)
	rInt.FillBytes(sig[:32])
	sInt.FillBytes(sig[32:])
	return sig, nil
}

func VerifyNodeInventory(r NodeInstanceInventory, keys nodeKeyLookup) error {
	if len(r.Signature) == 0 {
		return ErrEmptySignature
	}
	if keys == nil {
		return ErrUnknownNodeKey
	}
	key, ok := keys.PublicKeyForNode(r.NodeID, r.NodeKeyID)
	if !ok {
		return ErrUnknownNodeKey
	}
	if key.Curve != ecdsaP256() || !verifyDigestRaw(key, r.digest(), r.Signature) {
		return ErrSignatureMismatch
	}
	return nil
}
