package internalmail

import (
	"net/http"
	"strings"
)

func (s *Service) registerDomainLifecycle(mux *http.ServeMux) {
	base := OperationsPath + "/domains/{id}"
	mux.HandleFunc("GET "+base+"/keys", s.getDKIMKeys)
	mux.HandleFunc("POST "+base+"/keys/stage", s.postStageDKIM)
	mux.HandleFunc("POST "+base+"/keys/{key}/verify", s.postVerifyDKIM)
	mux.HandleFunc("POST "+base+"/keys/{key}/activate", s.postActivateDKIM)
	mux.HandleFunc("POST "+base+"/keys/{key}/retire", s.postRetireDKIM)
	mux.HandleFunc("POST "+base+"/reverify", s.postReverifyDomain)
}

func (s *Service) domainRouteDisabled(path string) bool {
	if !strings.HasPrefix(path, OperationsPath+"/domains/") {
		return false
	}
	parts := strings.Split(strings.TrimPrefix(path, OperationsPath+"/domains/"), "/")
	if len(parts) > 1 && parts[1] == "keys" {
		return !s.rotationEnabled()
	}
	return len(parts) == 2 && parts[1] == "reverify" && !s.reverificationEnabled()
}

func (s *Service) getDKIMKeys(w http.ResponseWriter, r *http.Request) {
	if !validOperatorID(w, r) {
		return
	}
	store, err := s.lifecycleStore(true, true)
	if err != nil {
		respond(w, nil, err, 0)
		return
	}
	o, err := operatorList(r, true, false)
	if err != nil {
		respond(w, nil, err, 0)
		return
	}
	page, err := store.operatorDKIMKeys(r.Context(), r.PathValue("id"), o)
	respond(w, page, err, http.StatusOK)
}

func domainAuditRequest(w http.ResponseWriter, r *http.Request, key bool) (OperatorAuditInput, bool) {
	if !validOperatorID(w, r) {
		return OperatorAuditInput{}, false
	}
	if key && !canonicalOperatorID(r.PathValue("key")) {
		respond(w, nil, ErrInvalid, 0)
		return OperatorAuditInput{}, false
	}
	var input OperatorAuditInput
	ok := decode(w, r, &input)
	return input, ok
}

func (s *Service) postStageDKIM(w http.ResponseWriter, r *http.Request) {
	input, ok := domainAuditRequest(w, r, false)
	if !ok {
		return
	}
	record, err := s.StageDKIMKey(r.Context(), r.PathValue("id"), input)
	respond(w, record, err, http.StatusOK)
}
func (s *Service) postVerifyDKIM(w http.ResponseWriter, r *http.Request) {
	input, ok := domainAuditRequest(w, r, true)
	if !ok {
		return
	}
	record, err := s.VerifyDKIMKey(r.Context(), r.PathValue("id"), r.PathValue("key"), input)
	respond(w, record, err, http.StatusOK)
}
func (s *Service) postActivateDKIM(w http.ResponseWriter, r *http.Request) {
	input, ok := domainAuditRequest(w, r, true)
	if !ok {
		return
	}
	record, err := s.ActivateDKIMKey(r.Context(), r.PathValue("id"), r.PathValue("key"), input)
	respond(w, record, err, http.StatusOK)
}
func (s *Service) postRetireDKIM(w http.ResponseWriter, r *http.Request) {
	if !validOperatorID(w, r) {
		return
	}
	if !canonicalOperatorID(r.PathValue("key")) {
		respond(w, nil, ErrInvalid, 0)
		return
	}
	var input struct {
		OperatorAuditInput
		ConfirmedQueueDrained bool `json:"confirmed_queue_drained"`
	}
	if !decode(w, r, &input) {
		return
	}
	record, err := s.RetireDKIMKey(r.Context(), r.PathValue("id"), r.PathValue("key"), input.OperatorAuditInput, input.ConfirmedQueueDrained)
	respond(w, record, err, http.StatusOK)
}
func (s *Service) postReverifyDomain(w http.ResponseWriter, r *http.Request) {
	input, ok := domainAuditRequest(w, r, false)
	if !ok {
		return
	}
	record, err := s.ReverifyDomain(r.Context(), r.PathValue("id"), input)
	respond(w, record, err, http.StatusOK)
}
