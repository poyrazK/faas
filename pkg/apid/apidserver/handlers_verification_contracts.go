package apidserver

// ADR-20261122220000: verification contracts. An app's gregale.verify.yaml
// is stored in its validated JSON form; imaged runs it against each
// candidate before promotion. The routes are released by the
// verification-contracts capability.

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/verifycontract"
)

func (s *server) verificationContracts(w http.ResponseWriter) (state.VerificationContractStore, bool) {
	store, ok := s.store.(state.VerificationContractStore)
	if !ok {
		api.WriteProblem(w, api.NewProblem(http.StatusServiceUnavailable, api.CodeCapacity,
			"Verification contracts unavailable", "this server cannot store verification contracts"))
	}
	return store, ok
}

func verificationContractResponse(c state.VerificationContract) api.VerificationContract {
	return api.VerificationContract{AppID: c.AppID, Contract: c.Contract, ContractSHA256: c.SHA256,
		CheckCount: c.CheckCount, Revision: c.Revision, UpdatedAt: c.UpdatedAt}
}

func (s *server) getVerificationContract(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	store, ok := s.verificationContracts(w)
	if !ok {
		return
	}
	c, err := store.AppVerificationContract(r.Context(), app.ID)
	if errors.Is(err, state.ErrNotFound) {
		s.notFound(w, "the app has no verification contract")
		return
	}
	if err != nil {
		logCustomerFailure(s.log, "read verification contract", err)
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError, "internal_error", "failed to read verification contract", api.DetailServerFailure))
		return
	}
	writeJSON(w, http.StatusOK, verificationContractResponse(c))
}

func (s *server) putVerificationContract(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	store, ok := s.verificationContracts(w)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, verifycontract.MaxContractBytes+4096)
	var req api.PutVerificationContractRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	contract, err := verifycontract.ParseJSON(req.Contract)
	if err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	raw, sum, err := contract.Canonical()
	if err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	stored, err := store.PutAppVerificationContract(r.Context(), state.VerificationContract{
		AppID: app.ID, AccountID: app.AccountID, Contract: raw, SHA256: sum, CheckCount: len(contract.Checks),
	}, req.ExpectedRevision)
	switch {
	case errors.Is(err, state.ErrVerificationContractRevision):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, "verification_contract_changed",
			"Verification contract changed", "the stored contract's revision is not expected_revision; read it again"))
		return
	case errors.Is(err, state.ErrNotFound):
		s.notFound(w, "no such app")
		return
	case err != nil:
		logCustomerFailure(s.log, "store verification contract", err)
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError, "internal_error", "failed to store verification contract", api.DetailServerFailure))
		return
	}
	s.audit.Emit(r.Context(), "app.verification_contract.replaced", &acct.ID, map[string]any{
		"app_id": app.ID, "contract_sha256": sum, "check_count": len(contract.Checks), "revision": stored.Revision,
	})
	writeJSON(w, http.StatusOK, verificationContractResponse(stored))
}

func (s *server) deleteVerificationContract(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	store, ok := s.verificationContracts(w)
	if !ok {
		return
	}
	var expected *int64
	if raw := r.URL.Query().Get("expected_revision"); raw != "" {
		revision, parseErr := strconv.ParseInt(raw, 10, 64)
		if parseErr != nil || revision < 1 {
			api.WriteProblem(w, api.ErrValidation("expected_revision must be a positive integer"))
			return
		}
		expected = &revision
	}
	err := store.DeleteAppVerificationContract(r.Context(), app.ID, app.AccountID, expected)
	if errors.Is(err, state.ErrVerificationContractRevision) {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, "verification_contract_changed",
			"Verification contract changed", "the stored contract's revision is not expected_revision; read it again"))
		return
	}
	if errors.Is(err, state.ErrNotFound) {
		s.notFound(w, "the app has no verification contract")
		return
	}
	if err != nil {
		logCustomerFailure(s.log, "delete verification contract", err)
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError, "internal_error", "failed to delete verification contract", api.DetailServerFailure))
		return
	}
	s.audit.Emit(r.Context(), "app.verification_contract.deleted", &acct.ID, map[string]any{"app_id": app.ID})
	w.WriteHeader(http.StatusNoContent)
}
