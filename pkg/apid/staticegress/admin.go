// Package staticegress serves the operator's static egress address
// inventory (ADR-20261128090000). An address is an additional public IPv4
// the provider routes to one compute node; the operator assigns it to an
// account, and that account's Scale apps may pin it. schedd places a pinned
// app on the address's node and vmmd on that node aliases it.
package staticegress

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/netip"
	"strings"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

// Store is the state the inventory routes need.
type Store interface {
	state.StaticEgressAddressStore
	ComputeNodeByName(ctx context.Context, name string) (state.ComputeNode, error)
	AccountByID(ctx context.Context, id string) (state.Account, error)
}

// Operations are the inventory routes.
type Operations struct {
	AdminAllows func(state.Account) (bool, *api.Problem)
	Store       Store
	Notifier    interface {
		Notify(ctx context.Context, channel, payload string) error
	}
	// Audit is optional.
	Audit interface {
		Emit(ctx context.Context, kind string, accountID *string, data map[string]any)
	}
	// Log records why a request failed; nil drops it.
	Log *slog.Logger
}

// Register serves the inventory. Reads use apid's operator read policy and
// writes the strict admin-mutation policy.
func (o Operations) Register(mux *http.ServeMux, trace func(http.Handler) http.Handler,
	auth func(httpkit.AccountHandler) http.HandlerFunc, read, mutation func(httpkit.AccountHandler) httpkit.AccountHandler) {
	mux.HandleFunc("GET /v1/admin/ops/static-egress-ips", auth(read(o.List)))
	mux.Handle("PUT /v1/admin/ops/static-egress-ips/{ip}", trace(auth(mutation(o.Put))))
	mux.Handle("DELETE /v1/admin/ops/static-egress-ips/{ip}", trace(auth(mutation(o.Delete))))
}

// List answers the whole inventory; it is a few rows per node.
func (o Operations) List(w http.ResponseWriter, r *http.Request, operator state.Account) {
	if !o.allowed(w, operator) {
		return
	}
	rows, err := o.Store.ListStaticEgressAddresses(r.Context())
	if err != nil {
		o.fail(w, "list static egress addresses", err)
		return
	}
	out := api.ObsStaticEgressAddressList{Addresses: make([]api.ObsStaticEgressAddress, 0, len(rows))}
	for _, row := range rows {
		out.Addresses = append(out.Addresses, view(row))
	}
	httpkit.WriteJSON(w, http.StatusOK, out)
}

// Put adds the address or replaces its node, account and note.
func (o Operations) Put(w http.ResponseWriter, r *http.Request, operator state.Account) {
	if !o.allowed(w, operator) {
		return
	}
	ip, ok := pathIP(w, r)
	if !ok {
		return
	}
	var req api.ObsPutStaticEgressAddressRequest
	if err := httpkit.DecodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid JSON body"))
		return
	}
	addr, prob := o.resolve(r.Context(), ip, req)
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	prior, err := o.Store.StaticEgressAddressByIP(r.Context(), ip)
	status := http.StatusOK
	switch {
	case errors.Is(err, state.ErrNotFound):
		status, err = http.StatusCreated, o.Store.AddStaticEgressAddress(r.Context(), addr)
	case err == nil:
		err = o.Store.UpdateStaticEgressAddress(r.Context(), addr)
	}
	if prob := writeError(err); prob != nil {
		if !errors.Is(err, state.ErrStaticEgressAddressPinned) && !errors.Is(err, state.ErrConflict) {
			o.logf("put static egress address", err)
		}
		api.WriteProblem(w, prob)
		return
	}
	saved, err := o.Store.StaticEgressAddressByIP(r.Context(), ip)
	if err != nil {
		o.fail(w, "read static egress address", err)
		return
	}
	o.changed(r.Context(), operator, "put", saved, prior.NodeID)
	httpkit.WriteJSON(w, status, view(saved))
}

// Delete retires an address no live app pins. ?confirm=true is required.
func (o Operations) Delete(w http.ResponseWriter, r *http.Request, operator state.Account) {
	if !o.allowed(w, operator) {
		return
	}
	ip, ok := pathIP(w, r)
	if !ok {
		return
	}
	if r.URL.Query().Get("confirm") != "true" {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "confirm required", "?confirm=true is required to remove a static egress address"))
		return
	}
	prior, err := o.Store.StaticEgressAddressByIP(r.Context(), ip)
	if err == nil {
		err = o.Store.RemoveStaticEgressAddress(r.Context(), ip)
	}
	if prob := writeError(err); prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	o.changed(r.Context(), operator, "delete", prior, "")
	w.WriteHeader(http.StatusNoContent)
}

func (o Operations) resolve(ctx context.Context, ip netip.Addr, req api.ObsPutStaticEgressAddressRequest) (state.StaticEgressAddress, *api.Problem) {
	name := strings.TrimSpace(req.Node)
	if name == "" {
		return state.StaticEgressAddress{}, api.ErrValidation("node is required: the compute node the provider routes this address to")
	}
	node, err := o.Store.ComputeNodeByName(ctx, name)
	if errors.Is(err, state.ErrNotFound) {
		return state.StaticEgressAddress{}, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Compute node not found", fmt.Sprintf("no compute node is named %q", name))
	} else if err != nil {
		return state.StaticEgressAddress{}, api.ErrCapacity("could not read the compute node")
	}
	account := strings.TrimSpace(req.AccountID)
	if account != "" {
		if _, err := uuid.Parse(account); err != nil {
			return state.StaticEgressAddress{}, api.ErrValidation("account_id must be a UUID")
		}
		if _, err := o.Store.AccountByID(ctx, account); errors.Is(err, state.ErrNotFound) {
			return state.StaticEgressAddress{}, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Account not found", "no account has this id")
		} else if err != nil {
			return state.StaticEgressAddress{}, api.ErrCapacity("could not read the account")
		}
	}
	if len(req.Note) > 200 {
		return state.StaticEgressAddress{}, api.ErrValidation("note is at most 200 bytes")
	}
	return state.StaticEgressAddress{IP: ip, NodeID: node.ID, AccountID: account, Note: req.Note}, nil
}

// changed wakes vmmd on the address's old and new node, then audits.
func (o Operations) changed(ctx context.Context, operator state.Account, action string, addr state.StaticEgressAddress, priorNode string) {
	if o.Notifier != nil {
		payload := fmt.Sprintf(`{"ip":%q,"node_id":%q,"prior_node_id":%q}`, addr.IP.String(), addr.NodeID, priorNode)
		if err := o.Notifier.Notify(ctx, db.NotifyStaticEgressAddresses, payload); err != nil {
			o.logf("notify static egress change", err)
		}
	}
	if o.Audit != nil {
		o.Audit.Emit(ctx, "admin.static_egress_address_"+action, &operator.ID, map[string]any{
			"ip": addr.IP.String(), "node_id": addr.NodeID, "prior_node_id": priorNode,
			"account_id": addr.AccountID, "pinned_app_id": addr.PinnedAppID,
		})
	}
}

func (o Operations) allowed(w http.ResponseWriter, operator state.Account) bool {
	if allowed, prob := o.AdminAllows(operator); !allowed {
		api.WriteProblem(w, prob)
		return false
	}
	if o.Store == nil {
		api.WriteProblem(w, api.ErrCapacity("the static egress address inventory is not available"))
		return false
	}
	return true
}

func (o Operations) fail(w http.ResponseWriter, op string, err error) {
	o.logf(op, err)
	api.WriteProblem(w, api.ErrCapacity("could not "+op))
}

func (o Operations) logf(op string, err error) {
	if o.Log != nil {
		o.Log.Warn("static egress inventory: "+op, "err", err)
	}
}

func pathIP(w http.ResponseWriter, r *http.Request) (netip.Addr, bool) {
	raw := r.PathValue("ip")
	ip, err := netip.ParseAddr(raw)
	if err != nil || !ip.Is4() {
		api.WriteProblem(w, api.ErrAppStaticEgressIPInvalid(raw, "must be a dotted-quad IPv4 address"))
		return netip.Addr{}, false
	}
	if err := api.ValidateStaticEgressIP(ip); err != nil {
		api.WriteProblem(w, api.ErrAppStaticEgressIPInvalid(raw, err.Error()))
		return netip.Addr{}, false
	}
	return ip, true
}

func writeError(err error) *api.Problem {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, state.ErrNotFound):
		return api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Static egress address not found", "the inventory has no such address")
	case errors.Is(err, state.ErrStaticEgressAddressPinned):
		return api.NewProblem(http.StatusConflict, api.CodeConflict, "Address is pinned",
			"a live app pins this address; clear its pin before removing the address or assigning it to another account")
	case errors.Is(err, state.ErrConflict):
		return api.NewProblem(http.StatusConflict, api.CodeConflict, "Address already exists", "the inventory already has this address")
	default:
		return api.ErrCapacity("could not change the static egress address")
	}
}

func view(a state.StaticEgressAddress) api.ObsStaticEgressAddress {
	return api.ObsStaticEgressAddress{IP: a.IP.String(), NodeID: a.NodeID, Node: a.NodeName, AccountID: a.AccountID,
		Note: a.Note, PinnedAppID: a.PinnedAppID, CreatedAt: a.CreatedAt, UpdatedAt: a.UpdatedAt}
}
