package agentproxy

import (
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"time"
)

// RegistrationRequest is the body of PUT /v1/registrations/{addr}.
type RegistrationRequest struct {
	AccountID string `json:"account_id"`
	SandboxID string `json:"sandbox_id"`
}

// RegistrationResponse is a stored registration.
type RegistrationResponse struct {
	Addr      string    `json:"addr"`
	AccountID string    `json:"account_id"`
	SandboxID string    `json:"sandbox_id"`
	Expires   time.Time `json:"expires"`
}

// Handler is the registration API vmmd calls over the agent tunnel
// (ADR-20261117185346 §4). It listens only on the gateway's tunnel address,
// so a request's source is a WireGuard peer, and a peer registers only
// addresses in its own node's slice.
func Handler(reg *Registry) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("PUT /v1/registrations/{addr}", func(w http.ResponseWriter, r *http.Request) {
		peer, addr, ok := registrationTarget(w, r)
		if !ok {
			return
		}
		var body RegistrationRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10)).Decode(&body); err != nil {
			http.Error(w, "body must be a registration", http.StatusBadRequest)
			return
		}
		stored, err := reg.Register(peer, addr, body.AccountID, body.SandboxID)
		if err != nil {
			registrationError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(RegistrationResponse{Addr: stored.Addr.String(), AccountID: stored.AccountID,
			SandboxID: stored.SandboxID, Expires: stored.Expires})
	})
	mux.HandleFunc("DELETE /v1/registrations/{addr}", func(w http.ResponseWriter, r *http.Request) {
		peer, addr, ok := registrationTarget(w, r)
		if !ok {
			return
		}
		if err := reg.Unregister(peer, addr); err != nil {
			registrationError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	return mux
}

func registrationTarget(w http.ResponseWriter, r *http.Request) (peer, addr netip.Addr, ok bool) {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		peer, err = netip.ParseAddr(host)
	}
	if err != nil {
		http.Error(w, "unknown peer", http.StatusForbidden)
		return netip.Addr{}, netip.Addr{}, false
	}
	addr, err = netip.ParseAddr(r.PathValue("addr"))
	if err != nil || !addr.Is4() {
		http.Error(w, "addr must be an IPv4 address", http.StatusBadRequest)
		return netip.Addr{}, netip.Addr{}, false
	}
	return peer.Unmap(), addr, true
}

func registrationError(w http.ResponseWriter, err error) {
	if errors.Is(err, ErrNotYourSlice) {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	http.Error(w, err.Error(), http.StatusBadRequest)
}
