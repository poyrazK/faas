// adr: 20261018183000
package operation

import (
	"context"
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
)

// childStore returns the child store when child Operations are open to the
// account. A closed account sees the routes as absent.
func (s *Service) childStore(w http.ResponseWriter, accountID string) (state.OperationChildStore, bool) {
	w.Header().Set("Cache-Control", "no-store")
	if !api.OperationChildrenEnabled(accountID) {
		httpkit.NoSuchRoute(w)
		return nil, false
	}
	store, ok := s.Host.Store().(state.OperationChildStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("child operation storage is unavailable"))
	}
	return store, ok
}

func writeChildError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, state.ErrOperationChildDepth):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, "operation_child_depth", "Child cannot start children", "only an Operation started directly may start child Operations"))
	case errors.Is(err, state.ErrOperationNotActive):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, "operation_not_active", "Operation has settled", "a settled or cancelled Operation starts no children"))
	case errors.Is(err, state.ErrInvalidArgument):
		api.WriteProblem(w, api.ErrValidation("operation names a definition of this deployment, key is 1 to 64 bytes, and input must match that definition's input schema"))
	default:
		WriteOperationError(w, err)
	}
}

func childResponses(in []state.OperationChild) api.OperationChildrenResponse {
	out := api.OperationChildrenResponse{Children: make([]api.OperationChild, 0, len(in))}
	for _, c := range in {
		out.Children = append(out.Children, api.OperationChild{Key: c.Key, Operation: c.Operation.OperationResponse})
	}
	return out
}

// StartRuntimeChildOperation lets a running HTTP Operation start a child for
// its customer. The parent's workload assertion and attempt capability are
// the only accepted credentials.
func (s *Service) StartRuntimeChildOperation(w http.ResponseWriter, r *http.Request) {
	authority, err := s.runtimeOperationAuthority(r)
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusUnauthorized, api.CodeUnauthorized, "Workload identity required", "provide a current operation workload assertion"))
		return
	}
	children, ok := s.childStore(w, authority.AccountID)
	if !ok {
		return
	}
	operations, ok := s.OperationStore(w)
	if !ok {
		return
	}
	var req api.OperationChildRequest
	if !DecodeOperationBody(w, r, &req, api.OperationChildBodyMaxBytes) {
		return
	}
	parent, err := operations.OperationByID(r.Context(), authority.AccountID, "", r.PathValue("id"))
	if err != nil {
		writeChildError(w, err)
		return
	}
	def, err := operations.OperationDefinitionForDeployment(r.Context(), parent.AccountID, parent.AppID, parent.DeploymentID, req.Operation)
	if err != nil {
		writeChildError(w, err)
		return
	}
	if !s.operationAdmissionOpen(w, parent.AccountID, def.AppID, def.Scope, parent.PlatformTenantID, def.Spec) {
		return
	}
	child, created, err := children.StartChildOperation(r.Context(), state.OperationChildStart{ParentID: parent.ID, Authority: authority, Request: req})
	if err != nil {
		writeChildError(w, err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
		s.Host.EmitAudit(r.Context(), "app.operation_child_started", &parent.AccountID, map[string]any{"parent_operation_id": parent.ID, "operation_id": child.Operation.ID, "key": child.Key, "platform_tenant_id": parent.PlatformTenantID})
	}
	httpkit.WriteJSON(w, status, api.OperationChildStartResponse{OperationChild: api.OperationChild{Key: child.Key, Operation: child.Operation.OperationResponse}, Replayed: !created})
}

// ListRuntimeOperationChildren lets the parent's current attempt read its
// children to decide when its fan-out is done.
func (s *Service) ListRuntimeOperationChildren(w http.ResponseWriter, r *http.Request) {
	parent, authority, ok := s.runtimeOperation(w, r)
	if !ok {
		return
	}
	children, ok := s.childStore(w, authority.AccountID)
	if ok {
		s.writeChildren(w, r, children, parent.AccountID, "", parent.ID)
	}
}

func (s *Service) ListPlatformTenantSelfOperationChildren(w http.ResponseWriter, r *http.Request, acct state.Account) {
	children, ok := s.childStore(w, acct.ID)
	if !ok {
		return
	}
	tenant, ok := s.activePlatformTenantSelfID(w, r, acct)
	if ok {
		s.writeChildren(w, r, children, acct.ID, tenant, r.PathValue("id"))
	}
}

func (s *Service) ListOwnedOperationChildren(w http.ResponseWriter, r *http.Request, acct state.Account) {
	children, ok := s.childStore(w, acct.ID)
	if !ok {
		return
	}
	op, _, ok := s.ownedOperation(w, r, acct)
	if ok {
		s.writeChildren(w, r, children, acct.ID, "", op.ID)
	}
}

func (s *Service) writeChildren(w http.ResponseWriter, r *http.Request, store state.OperationChildStore, accountID, tenantID, parentID string) {
	list, err := store.OperationChildren(r.Context(), accountID, tenantID, parentID)
	if err != nil {
		writeChildError(w, err)
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, childResponses(list))
}

// cancelOperationChildren cancels the active children of a parent whose
// cancellation just committed. A child start takes the parent's execution
// lock, so none starts after that commit; repeating the parent's cancel
// finishes a cascade this process did not.
func (s *Service) cancelOperationChildren(ctx context.Context, store state.OperationStore, accountID, tenantID, parentID string) {
	if !api.OperationChildrenEnabled(accountID) {
		return
	}
	children, ok := s.Host.Store().(state.OperationChildStore)
	if !ok {
		return
	}
	list, err := children.OperationChildren(ctx, accountID, tenantID, parentID)
	if err != nil {
		s.Host.Logger().Warn("child operation cascade skipped", "parent_operation_id", parentID, "err", err)
		return
	}
	for _, child := range list {
		if child.Operation.State.Terminal() || child.Operation.CancellationRequested {
			continue
		}
		if _, err := store.CancelOperation(ctx, accountID, tenantID, child.Operation.ID, child.Operation.Generation); err != nil {
			s.Host.Logger().Warn("child operation cancel failed", "parent_operation_id", parentID, "operation_id", child.Operation.ID, "err", err)
		}
	}
}
