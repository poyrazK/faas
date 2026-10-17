package internalmail

import (
	"net/http"
	"net/url"
	"strconv"

	"github.com/onebox-faas/faas/pkg/api"
)

func (s *Service) registerProducerOutbox(mux *http.ServeMux) {
	mux.HandleFunc("GET "+ProducerOutboxPath, s.getProducerOutboxCounts)
	mux.HandleFunc("GET "+ProducerOutboxPath+"/items", s.getProducerOutboxItems)
	mux.HandleFunc("GET "+ProducerOutboxPath+"/items/{id}", s.getProducerOutboxItem)
	mux.HandleFunc("GET "+ProducerOutboxPath+"/items/{id}/admission", s.getProducerOutboxAdmission)
}

func (s *Service) getProducerOutboxAdmission(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" {
		respond(w, nil, ErrInvalid, 0)
		return
	}
	review, err := s.ProducerOutboxAdmission(r.Context(), r.PathValue("id"))
	respond(w, review, err, http.StatusOK)
}

func (s *Service) getProducerOutboxCounts(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" {
		respond(w, nil, ErrInvalid, 0)
		return
	}
	counts, err := s.ProducerOutboxCounts(r.Context())
	respond(w, counts, err, http.StatusOK)
}

func (s *Service) getProducerOutboxItems(w http.ResponseWriter, r *http.Request) {
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		respond(w, nil, ErrInvalid, 0)
		return
	}
	for name, v := range values {
		if len(v) != 1 || name != "limit" && name != "after" && name != "status" {
			respond(w, nil, ErrInvalid, 0)
			return
		}
	}
	list := OperatorList{Limit: api.InternalMailOperatorPageSize, After: values.Get("after")}
	if raw, ok := values["limit"]; ok {
		list.Limit, err = strconv.Atoi(raw[0])
	}
	list.Status = values.Get("status")
	if err != nil || !validProducerOutboxList(list) {
		respond(w, nil, ErrInvalid, 0)
		return
	}
	page, err := s.ProducerOutboxItems(r.Context(), list)
	respond(w, page, err, http.StatusOK)
}

func (s *Service) getProducerOutboxItem(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" {
		respond(w, nil, ErrInvalid, 0)
		return
	}
	item, err := s.ProducerOutboxItem(r.Context(), r.PathValue("id"))
	respond(w, item, err, http.StatusOK)
}
