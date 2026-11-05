package realtime

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"
)

// A restarted realtimed used to start with no endpoints and learn them from
// apid's endpoint reconcile, every 30 seconds, so every reconnect after a
// deploy was refused with 404 until then (ADR-1323, amendment of
// 2026-11-03). The process now saves its registrations on each change and a
// new process restores them before it reports ready. apid's reconcile still
// corrects whatever changed while no process was running.

// endpointSnapshotVersion is the snapshot format.
const endpointSnapshotVersion = 1

type endpointSnapshot struct {
	Version   int               `json:"version"`
	Endpoints []endpointRequest `json:"endpoints"`
}

func (m *Manager) endpointsChanged() {
	if m != nil && m.cfg.EndpointsChanged != nil {
		m.cfg.EndpointsChanged()
	}
}

// EndpointSnapshot returns every registered endpoint, credentials included,
// in the management API's form.
func (m *Manager) EndpointSnapshot() ([]byte, error) {
	if m == nil {
		return nil, errors.New("realtime: no manager")
	}
	snapshot := endpointSnapshot{Version: endpointSnapshotVersion, Endpoints: []endpointRequest{}}
	m.endpoints.Range(func(_, value any) bool {
		if e := value.(*endpointState).config.Load(); e != nil {
			snapshot.Endpoints = append(snapshot.Endpoints, endpointRequestFor(*e))
		}
		return true
	})
	sort.Slice(snapshot.Endpoints, func(i, j int) bool { return snapshot.Endpoints[i].ID < snapshot.Endpoints[j].ID })
	return json.Marshal(snapshot)
}

// RestoreEndpoints registers the endpoints of a snapshot and reports how many
// it registered. An endpoint the snapshot cannot register is skipped; apid's
// reconcile registers it again.
func (m *Manager) RestoreEndpoints(data []byte) (int, error) {
	var snapshot endpointSnapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return 0, fmt.Errorf("realtime: decode endpoint snapshot: %w", err)
	}
	if snapshot.Version != endpointSnapshotVersion {
		return 0, fmt.Errorf("realtime: endpoint snapshot version %d", snapshot.Version)
	}
	restored := 0
	var errs []error
	for _, request := range snapshot.Endpoints {
		if err := m.registerEndpoint(request.endpoint()); err != nil {
			errs = append(errs, fmt.Errorf("endpoint %s: %w", request.ID, err))
			continue
		}
		restored++
	}
	return restored, errors.Join(errs...)
}

// EndpointFingerprint identifies an endpoint registration's content: two
// registrations with the same fingerprint configure a node the same way. It
// also changes when the previous auth token's grace ends, because a node
// closes that token's sockets when the endpoint is registered again.
func EndpointFingerprint(e Endpoint, now time.Time) string {
	data, err := json.Marshal(endpointRequestFor(e))
	if err != nil {
		return ""
	}
	h := sha256.New()
	h.Write(data)
	if !e.AuthTokenPreviousExpiresAt.IsZero() && !now.Before(e.AuthTokenPreviousExpiresAt) {
		h.Write([]byte{1})
	}
	return hex.EncodeToString(h.Sum(nil))
}
