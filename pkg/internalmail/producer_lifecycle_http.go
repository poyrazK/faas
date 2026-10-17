package internalmail

import (
	"net/http"
	"strings"
)

func producerLifecycleRoute(path string) bool {
	return path == ProducerLifecyclePath || strings.HasPrefix(path, ProducerLifecyclePath+"/")
}

func (s *Service) registerProducerLifecycle(mux *http.ServeMux) {
	mux.HandleFunc("GET "+ProducerLifecyclePath+"/{producer}", s.getProducerAccess)
	mux.HandleFunc("POST "+ProducerLifecyclePath+"/{action}", s.postProducerAccess)
}

func (s *Service) getProducerAccess(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" {
		respond(w, nil, ErrInvalid, 0)
		return
	}
	status, err := s.ProducerAccessStatus(r.Context(), r.PathValue("producer"))
	respond(w, status, err, http.StatusOK)
}

func (s *Service) postProducerAccess(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" {
		respond(w, nil, ErrInvalid, 0)
		return
	}
	var input ProducerLifecycleRequest
	if !decode(w, r, &input) {
		return
	}
	record, err := s.ChangeProducerAccess(r.Context(), r.PathValue("action"), input)
	respond(w, record, err, http.StatusOK)
}
