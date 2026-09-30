// adr: 375
package gateway

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/realtime"
	"github.com/onebox-faas/faas/pkg/reqbudget"
	"github.com/onebox-faas/faas/pkg/trafficrevocation"
)

const (
	trafficSecurityHeader   = "X-Faas-Traffic-Security"
	trafficSecurityRealtime = "v1;managed-realtime"
)

type securityScopeWire struct {
	Kind     string `json:"kind"`
	ID       string `json:"id"`
	Revision int64  `json:"revision"`
}

// Only the protected compute hop may author this canonical, bounded snapshot.
// It carries no credentials and grants no access without an authoritative read.
func encodeTrafficSecurity(states map[trafficrevocation.Scope]trafficrevocation.State) (string, error) {
	if len(states) == 0 || len(states) > api.TrafficSecurityMaxRequestScopes {
		return "", trafficrevocation.ErrUnavailable
	}
	rows := make([]securityScopeWire, 0, len(states))
	for scope, state := range states {
		id, err := uuid.Parse(scope.ID)
		if err != nil || id == uuid.Nil || scope.ID != id.String() || state.Revision < 0 || state.Revoked ||
			(scope.Kind != "account" && scope.Kind != "app" && scope.Kind != "deployment") {
			return "", trafficrevocation.ErrUnavailable
		}
		rows = append(rows, securityScopeWire{scope.Kind, scope.ID, state.Revision})
	}
	sort.Slice(rows, func(i, j int) bool {
		return rows[i].Kind < rows[j].Kind || rows[i].Kind == rows[j].Kind && rows[i].ID < rows[j].ID
	})
	data, err := json.Marshal(rows)
	if err != nil {
		return "", err
	}
	value := "v1." + base64.RawURLEncoding.EncodeToString(data)
	if len(value) > api.TrafficSecurityMaxHeaderBytes {
		return "", trafficrevocation.ErrUnavailable
	}
	return value, nil
}

func decodeTrafficSecurity(value string) (map[trafficrevocation.Scope]trafficrevocation.State, error) {
	if len(value) > api.TrafficSecurityMaxHeaderBytes || !strings.HasPrefix(value, "v1.") {
		return nil, trafficrevocation.ErrUnavailable
	}
	data, err := base64.RawURLEncoding.Strict().DecodeString(strings.TrimPrefix(value, "v1."))
	if err != nil {
		return nil, trafficrevocation.ErrUnavailable
	}
	var rows []securityScopeWire
	if err := json.Unmarshal(data, &rows); err != nil || len(rows) == 0 || len(rows) > api.TrafficSecurityMaxRequestScopes {
		return nil, trafficrevocation.ErrUnavailable
	}
	states := make(map[trafficrevocation.Scope]trafficrevocation.State, len(rows))
	for _, row := range rows {
		scope := trafficrevocation.Scope{Kind: row.Kind, ID: row.ID}
		if _, duplicate := states[scope]; duplicate {
			return nil, trafficrevocation.ErrUnavailable
		}
		states[scope] = trafficrevocation.State{Revision: row.Revision}
	}
	canonical, err := encodeTrafficSecurity(states)
	if err != nil || canonical != value {
		return nil, trafficrevocation.ErrUnavailable
	}
	return states, nil
}

func stampTrafficSecurity(ctx context.Context, header http.Header) {
	header.Del(trafficSecurityHeader)
	// Invalid owner IDs can occur only in legacy fixtures. A production public
	// gateway refuses a successful response without this verified snapshot.
	if value, err := encodeTrafficSecurity(trafficSecuritySnapshot(ctx)); err == nil {
		header.Set(trafficSecurityHeader, value)
	}
}

type publicTrafficSecurityKey struct{}

type publicTrafficSecurity struct {
	registry *trafficrevocation.Registry
	cancel   context.CancelCauseFunc
	release  func()
}

func (p *InternalReverseProxy) WithTrafficRevocations(registry *trafficrevocation.Registry) *InternalReverseProxy {
	p.trafficRevocations = registry
	return p
}

func (p *InternalReverseProxy) publicTrafficContext(parent context.Context) (context.Context, func()) {
	if p.trafficRevocations == nil { // Legacy fixtures only; production wires it.
		return parent, func() {}
	}
	ctx, cancel := reqbudget.WithCancellationFence(parent)
	owner := &publicTrafficSecurity{registry: p.trafficRevocations, cancel: cancel}
	ctx = context.WithValue(ctx, publicTrafficSecurityKey{}, owner)
	return ctx, func() {
		if owner.release != nil {
			owner.release()
		}
		cancel(nil)
	}
}

func bindPublicTrafficSecurity(r *http.Request, resp *http.Response) error {
	values := resp.Header.Values(trafficSecurityHeader)
	resp.Header.Del(trafficSecurityHeader)
	stripTrafficControlTrailerDeclarations(resp.Header)
	stripTrafficControlTrailers(resp.Trailer)
	owner, _ := r.Context().Value(publicTrafficSecurityKey{}).(*publicTrafficSecurity)
	if owner == nil {
		return nil
	}
	if len(values) == 0 && resp.StatusCode >= http.StatusBadRequest && !isLongLivedResponse(resp.StatusCode, resp.Header) {
		return nil // No owner exists for a pre-admission platform error.
	}
	if len(values) != 1 {
		return trafficrevocation.ErrUnavailable
	}
	if values[0] == trafficSecurityRealtime {
		if strings.HasPrefix(r.URL.Path, realtime.ManagedPathPrefix) {
			return nil // Explicitly excluded, separate managed connection owner.
		}
		return trafficrevocation.ErrUnavailable
	}
	states, err := decodeTrafficSecurity(values[0])
	if err != nil {
		return err
	}
	owner.release, err = owner.registry.AdmitAt(r.Context(), states, owner.cancel)
	if err != nil {
		return err
	}
	if cause := trafficRevocationCause(r.Context()); cause != nil {
		return cause
	}
	return r.Context().Err()
}

func refusePublicTrafficSecurity(w http.ResponseWriter, r *http.Request, err error) bool {
	if errors.Is(err, trafficrevocation.ErrUnavailable) || errors.Is(err, trafficrevocation.ErrRevoked) || errors.Is(err, trafficrevocation.ErrCapacity) {
		writeTrafficRevocationError(w, r, err)
		return true
	}
	return handleForwardRequestCancellation(w, r, true)
}
