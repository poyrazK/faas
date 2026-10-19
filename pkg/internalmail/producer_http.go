package internalmail

import (
	"net/http"
	"strings"
)

func (s *Service) registerProducers(mux *http.ServeMux) {
	base := ProducersPath + "/{producer}"
	mux.HandleFunc("POST "+base+"/messages", s.postProducedMessage)
	mux.HandleFunc("GET "+base+"/messages/{id}", s.getProducedMessage)
	mux.HandleFunc("GET "+base+"/messages/{id}/events", s.getProducedEvents)
	mux.HandleFunc("GET "+base+"/usage", s.getProducerUsage)
}

func producerRoute(path string) bool {
	return path == ProducersPath || strings.HasPrefix(path, ProducersPath+"/")
}

func (s *Service) postProducedMessage(w http.ResponseWriter, r *http.Request) {
	ctx, err := s.producerOutboxContext(r.Context(), r.Header)
	if err != nil {
		respond(w, nil, err, 0)
		return
	}
	var input SendRequest
	if !decode(w, r, &input) {
		return
	}
	if len(r.Header.Values("Idempotency-Key")) != 1 {
		respond(w, nil, ErrInvalid, 0)
		return
	}
	m, err := s.SubmitProduced(ctx, r.PathValue("producer"), r.Header.Get("Idempotency-Key"), input)
	respond(w, m, err, http.StatusAccepted)
}

func (s *Service) getProducedMessage(w http.ResponseWriter, r *http.Request) {
	m, err := s.ProducerMessage(r.Context(), r.PathValue("producer"), r.PathValue("id"))
	respond(w, m, err, http.StatusOK)
}

func (s *Service) getProducedEvents(w http.ResponseWriter, r *http.Request) {
	o, err := operatorList(r, false, false)
	if err != nil {
		respond(w, nil, err, 0)
		return
	}
	events, err := s.ProducerEvents(r.Context(), r.PathValue("producer"), r.PathValue("id"), o)
	respond(w, events, err, http.StatusOK)
}

func (s *Service) getProducerUsage(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" {
		respond(w, nil, ErrInvalid, 0)
		return
	}
	usage, err := s.ProducerUsage(r.Context(), r.PathValue("producer"))
	respond(w, usage, err, http.StatusOK)
}
