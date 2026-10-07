// adr: 678
package durableentity

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

// The immutable, path-compressed radix index is rooted in the state snapshot.
// A leaf is one original receipt; branches copy only the modified search path.
// SHA-256 paths bound reads to 65 nodes regardless of the receipt count.
type journalNode struct {
	Schema    int                   `json:"schema"`
	ID        ID                    `json:"entity"`
	Prefix    string                `json:"prefix"`
	RequestID string                `json:"request_id,omitempty"`
	Receipt   *receipt              `json:"receipt,omitempty"`
	Children  map[string]journalRef `json:"children,omitempty"`
}

type legacyReceipts struct {
	Schema   int                `json:"schema"`
	ID       ID                 `json:"entity"`
	Version  uint64             `json:"version"`
	Receipts map[string]receipt `json:"receipts"`
}

func validDigest(value string) bool { return len(value) == sha256HexLength && validHexPrefix(value) }

func validHexPrefix(value string) bool {
	for _, c := range value {
		if !strings.ContainsRune("0123456789abcdef", c) {
			return false
		}
	}
	return len(value) <= sha256HexLength
}

func generationNumber(value string) (uint64, bool) {
	n, err := strconv.ParseUint(value, 10, 64)
	return n, err == nil && strconv.FormatUint(n, 10) == value
}

func snapshotGeneration(id ID, key string) (uint64, bool) {
	path := strings.TrimPrefix(key, id.prefix()+"snapshots/")
	if path == key {
		return 0, false
	}
	parts := strings.Split(path, "/")
	if len(parts) == 1 {
		return 0, validUUID(strings.TrimSuffix(parts[0], ".json")) && strings.HasSuffix(parts[0], ".json")
	}
	if len(parts) != 2 || !strings.HasSuffix(parts[1], ".json") || !validUUID(strings.TrimSuffix(parts[1], ".json")) {
		return 0, false
	}
	return generationNumber(parts[0])
}

// Receipt paths carry the hash prefix so cleanup can prove reachability without
// reading an uncommitted candidate or trusting its contents.
func receiptPath(id ID, key string) (generation uint64, prefix string, legacy, ok bool) {
	path := strings.TrimPrefix(key, id.prefix()+"receipts/")
	if path == key {
		return
	}
	parts := strings.Split(path, "/")
	if len(parts) != 3 && len(parts) != 4 {
		return
	}
	generation, ok = generationNumber(parts[0])
	if !ok {
		return
	}
	name := parts[len(parts)-1]
	if !strings.HasSuffix(name, ".json") || !validUUID(strings.TrimSuffix(name, ".json")) {
		ok = false
		return
	}
	legacy = len(parts) == 3 && parts[1] == "legacy"
	if legacy {
		return
	}
	if len(parts) != 4 || parts[1] != "nodes" {
		ok = false
		return
	}
	prefix = parts[2]
	if prefix == "root" {
		prefix = ""
	} else if prefix == "" || !validHexPrefix(prefix) {
		ok = false
	}
	return
}

func validJournalRef(id ID, ref *journalRef, maxGeneration uint64) bool {
	if ref == nil {
		return true
	}
	generation, prefix, legacy, ok := receiptPath(id, ref.Key)
	return ok && !legacy && generation <= maxGeneration && prefix == ref.Prefix && validDigest(ref.Hash)
}

func validLegacyRef(id ID, ref *objectRef, maxGeneration uint64) bool {
	if ref == nil {
		return true
	}
	generation, _, legacy, ok := receiptPath(id, ref.Key)
	return ok && legacy && generation <= maxGeneration && validDigest(ref.Hash)
}

func validReceipt(r receipt, version uint64) bool {
	return validDigest(r.Fingerprint) && json.Valid(r.Result) && r.Version != 0 && r.Version <= version
}

func validReceipts(receipts map[string]receipt, version uint64) bool {
	if receipts == nil || len(receipts) > api.MaxDurableEntityReceipts {
		return false
	}
	for id, r := range receipts {
		if !validIdentity(id) || !validReceipt(r, version) {
			return false
		}
	}
	return true
}

func (m *Manager) readJournal(ctx context.Context, id ID, ref journalRef, version uint64) (journalNode, error) {
	node, _, err := m.readJournalSized(ctx, id, ref, version)
	return node, err
}

func (m *Manager) readJournalSized(ctx context.Context, id ID, ref journalRef, version uint64) (journalNode, int64, error) {
	limit := api.MaxDurableEntityJournalBytes
	if len(ref.Prefix) == sha256HexLength {
		limit = api.MaxDurableEntityReceiptBytes
	}
	body, _, err := m.store.Get(ctx, ref.Key, int64(limit))
	if err != nil {
		return journalNode{}, 0, errorsForRestore(err)
	}
	var node journalNode
	generation, _, _, _ := receiptPath(id, ref.Key)
	if len(body) > limit || digest(body) != ref.Hash || json.Unmarshal(body, &node) != nil || node.Schema != 1 || node.ID != id || node.Prefix != ref.Prefix {
		return journalNode{}, 0, ErrCorrupt
	}
	if node.Receipt != nil {
		if len(node.Children) != 0 || !validIdentity(node.RequestID) || digest([]byte(node.RequestID)) != node.Prefix || !validReceipt(*node.Receipt, version) {
			return journalNode{}, 0, ErrCorrupt
		}
	} else {
		if node.RequestID != "" || len(node.Prefix) >= sha256HexLength || len(node.Children) < 2 || len(node.Children) > len("0123456789abcdef") {
			return journalNode{}, 0, ErrCorrupt
		}
		for nibble, child := range node.Children {
			if len(nibble) != 1 || !validHexPrefix(nibble) || !validJournalRef(id, &child, generation) || !strings.HasPrefix(child.Prefix, node.Prefix+nibble) {
				return journalNode{}, 0, ErrCorrupt
			}
		}
	}
	return node, int64(len(body)), nil
}

func (m *Manager) findReceipt(ctx context.Context, state snapshot, requestID string) (receipt, bool, error) {
	if r, ok := state.Receipts[requestID]; ok {
		return r, true, nil
	}
	path, ref := digest([]byte(requestID)), state.ReceiptRoot
	for ref != nil && strings.HasPrefix(path, ref.Prefix) {
		node, err := m.readJournal(ctx, state.ID, *ref, state.Version)
		if err != nil {
			return receipt{}, false, err
		}
		if node.Receipt != nil {
			if node.RequestID != requestID {
				if path == ref.Prefix {
					return receipt{}, false, ErrCorrupt
				}
				break
			}
			return *node.Receipt, true, nil
		}
		child, ok := node.Children[path[len(node.Prefix):len(node.Prefix)+1]]
		if !ok {
			break
		}
		ref = &child
	}
	if state.LegacyReceipts == nil {
		return receipt{}, false, nil
	}
	legacy, err := m.readLegacyReceipts(ctx, state)
	if err != nil {
		return receipt{}, false, err
	}
	r, ok := legacy.Receipts[requestID]
	return r, ok, nil
}

func (m *Manager) readLegacyReceipts(ctx context.Context, state snapshot) (legacyReceipts, error) {
	block, _, err := m.readLegacySized(ctx, state)
	return block, err
}

func (m *Manager) readLegacySized(ctx context.Context, state snapshot) (legacyReceipts, int64, error) {
	body, _, err := m.store.Get(ctx, state.LegacyReceipts.Key, api.MaxDurableEntitySnapshotBytes)
	if err != nil {
		return legacyReceipts{}, 0, errorsForRestore(err)
	}
	var block legacyReceipts
	if len(body) > api.MaxDurableEntitySnapshotBytes || digest(body) != state.LegacyReceipts.Hash || json.Unmarshal(body, &block) != nil || block.Schema != 1 || block.ID != state.ID || block.Version > state.Version || !validReceipts(block.Receipts, block.Version) {
		return legacyReceipts{}, 0, ErrCorrupt
	}
	return block, int64(len(body)), nil
}

func (m *Manager) writeJournal(ctx context.Context, id ID, generation uint64, node journalNode) (journalRef, error) {
	prefix := node.Prefix
	if prefix == "" {
		prefix = "root"
	}
	key := fmt.Sprintf("%sreceipts/%d/nodes/%s/%s.json", id.prefix(), generation, prefix, uuid.NewString())
	limit := api.MaxDurableEntityJournalBytes
	if node.Receipt != nil {
		limit = api.MaxDurableEntityReceiptBytes
	}
	ref, err := m.writeImmutable(ctx, key, node, limit)
	return journalRef{objectRef: ref, Prefix: node.Prefix}, err
}

func (m *Manager) writeImmutable(ctx context.Context, key string, value any, limit int) (objectRef, error) {
	body, err := json.Marshal(value)
	if err != nil {
		return objectRef{}, fmt.Errorf("encode entity object: %w", err)
	}
	if len(body) > limit {
		return objectRef{}, exceeded("object_bytes", limit, len(body))
	}
	if _, err := m.store.Put(ctx, key, body, ""); err != nil {
		return objectRef{}, fmt.Errorf("upload uncommitted entity object: %w", err)
	}
	return objectRef{Key: key, Hash: digest(body), bytes: int64(len(body))}, nil
}

func commonPrefix(a, b string) string {
	i := 0
	for i < len(a) && i < len(b) && a[i] == b[i] {
		i++
	}
	return a[:i]
}

func (m *Manager) insertReceipt(ctx context.Context, id ID, generation, version uint64, root *journalRef, leaf journalRef) (journalRef, int64, error) {
	if root == nil {
		return leaf, 0, nil
	}
	prefix := commonPrefix(root.Prefix, leaf.Prefix)
	if prefix != root.Prefix {
		ref, err := m.writeJournal(ctx, id, generation, journalNode{Schema: 1, ID: id, Prefix: prefix, Children: map[string]journalRef{root.Prefix[len(prefix) : len(prefix)+1]: *root, leaf.Prefix[len(prefix) : len(prefix)+1]: leaf}})
		return ref, ref.bytes, err
	}
	if len(prefix) == sha256HexLength {
		return journalRef{}, 0, ErrRequestConflict
	}
	node, oldBytes, err := m.readJournalSized(ctx, id, *root, version)
	if err != nil {
		return journalRef{}, 0, err
	}
	nibble := leaf.Prefix[len(prefix) : len(prefix)+1]
	child, ok := node.Children[nibble]
	var next journalRef
	var delta int64
	if ok {
		next, delta, err = m.insertReceipt(ctx, id, generation, version, &child, leaf)
	} else {
		next = leaf
	}
	if err != nil {
		return journalRef{}, 0, err
	}
	node.Children[nibble] = next
	ref, err := m.writeJournal(ctx, id, generation, node)
	return ref, delta + ref.bytes - oldBytes, err
}

type journalDelta struct {
	leafBytes, indexBytes, legacyBytes int64
}

func (m *Manager) journalTransition(ctx context.Context, base manifest, state *snapshot, requestID string, saved receipt) (journalDelta, error) {
	var delta journalDelta
	if state.Schema == 1 && len(state.Receipts) != 0 {
		key := fmt.Sprintf("%sreceipts/%d/legacy/%s.json", base.ID.prefix(), base.Generation, uuid.NewString())
		ref, err := m.writeImmutable(ctx, key, legacyReceipts{Schema: 1, ID: base.ID, Version: base.Version, Receipts: state.Receipts}, api.MaxDurableEntitySnapshotBytes)
		if err != nil {
			return delta, err
		}
		state.LegacyReceipts = &ref
		delta.legacyBytes = ref.bytes
	}
	state.Schema, state.Receipts = 2, nil
	leaf, err := m.writeJournal(ctx, base.ID, base.Generation, journalNode{Schema: 1, ID: base.ID, Prefix: digest([]byte(requestID)), RequestID: requestID, Receipt: &saved})
	if err != nil {
		return delta, err
	}
	delta.leafBytes = leaf.bytes
	root, indexBytes, err := m.insertReceipt(ctx, base.ID, base.Generation, state.Version, state.ReceiptRoot, leaf)
	if err != nil {
		return delta, err
	}
	delta.indexBytes = indexBytes
	state.ReceiptRoot = &root
	return delta, nil
}
