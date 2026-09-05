package state

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

func runtimeConfigAckMapKey(ack RuntimeConfigAck) string {
	return fmt.Sprintf("%s\x00%s\x00%s\x00%s\x00%s", ack.Key, ack.Scope, ack.ScopeID, ack.Consumer, ack.NodeID)
}

// AcknowledgeRuntimeConfig records the result observed by one daemon/node.
// It is intentionally not part of the broad Store interface yet; older test
// doubles can continue implementing Store while runtimeconfig.Watcher uses
// this capability when available.
func (m *MemStore) AcknowledgeRuntimeConfig(_ context.Context, ack RuntimeConfigAck) error {
	if ack.Key == "" || ack.Consumer == "" || ack.Version <= 0 {
		return fmt.Errorf("state: invalid runtime config acknowledgement identity")
	}
	if ack.Status != RuntimeConfigAckApplied && ack.Status != RuntimeConfigAckFailed {
		return fmt.Errorf("state: invalid runtime config acknowledgement status %q", ack.Status)
	}
	ack.EffectiveValue = append(json.RawMessage(nil), ack.EffectiveValue...)
	ack.UpdatedAt = ack.UpdatedAt.UTC()
	if ack.UpdatedAt.IsZero() {
		ack.UpdatedAt = time.Now().UTC()
	}
	if ack.AppliedAt != nil {
		value := ack.AppliedAt.UTC()
		ack.AppliedAt = &value
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.runtimeConfigAcks == nil {
		m.runtimeConfigAcks = make(map[string]RuntimeConfigAck)
	}
	m.runtimeConfigAcks[runtimeConfigAckMapKey(ack)] = cloneRuntimeConfigAck(ack)
	return nil
}

func (m *MemStore) ListRuntimeConfigAcks(_ context.Context, key string, scope RuntimeConfigScope, scopeID string) ([]RuntimeConfigAck, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []RuntimeConfigAck
	for _, ack := range m.runtimeConfigAcks {
		if key != "" && ack.Key != key {
			continue
		}
		if scope != "" && ack.Scope != scope {
			continue
		}
		if scopeID != "" && ack.ScopeID != scopeID {
			continue
		}
		out = append(out, cloneRuntimeConfigAck(ack))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Key != out[j].Key {
			return out[i].Key < out[j].Key
		}
		if out[i].Consumer != out[j].Consumer {
			return out[i].Consumer < out[j].Consumer
		}
		return out[i].NodeID < out[j].NodeID
	})
	return out, nil
}

func cloneRuntimeConfigAck(ack RuntimeConfigAck) RuntimeConfigAck {
	ack.EffectiveValue = append(json.RawMessage(nil), ack.EffectiveValue...)
	if ack.AppliedAt != nil {
		value := *ack.AppliedAt
		ack.AppliedAt = &value
	}
	return ack
}
