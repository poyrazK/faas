package internalmail

import (
	"net/http"
	"net/url"
	"strings"
)

func reputationRoute(path string) bool {
	return path == ReputationPath || strings.HasPrefix(path, ReputationPath+"/")
}
func (s *Service) reputationRouteDisabled(path string) bool {
	return reputationRoute(path) && !s.reputationEnabled()
}
func (s *Service) registerReputation(mux *http.ServeMux) {
	mux.HandleFunc("GET "+ReputationPath, s.getReputation)
	mux.HandleFunc("GET "+ReputationPath+"/holds", s.getReputationHolds)
	mux.HandleFunc("GET "+ReputationPath+"/events", s.getReputationEvents)
	mux.HandleFunc("POST "+ReputationPath+"/release", s.postReputationRelease)
}
func (s *Service) getReputation(w http.ResponseWriter, r *http.Request) {
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || len(values) != 2 || len(values["scope"]) != 1 || len(values["key"]) != 1 {
		respond(w, nil, ErrInvalid, 0)
		return
	}
	status, err := s.ReputationStatus(r.Context(), ReputationScope{Kind: values.Get("scope"), Key: values.Get("key")})
	respond(w, status, err, http.StatusOK)
}
func (s *Service) getReputationHolds(w http.ResponseWriter, r *http.Request) {
	store, err := s.reputationOperations()
	if err != nil {
		respond(w, nil, err, 0)
		return
	}
	list, err := operatorList(r, false, false)
	if err != nil {
		respond(w, nil, err, 0)
		return
	}
	page, err := store.reputationHolds(r.Context(), list)
	respond(w, page, err, http.StatusOK)
}
func (s *Service) getReputationEvents(w http.ResponseWriter, r *http.Request) {
	store, err := s.reputationOperations()
	if err != nil {
		respond(w, nil, err, 0)
		return
	}
	list, err := operatorList(r, true, false)
	if err != nil {
		respond(w, nil, err, 0)
		return
	}
	page, err := store.reputationEvents(r.Context(), list)
	respond(w, page, err, http.StatusOK)
}
func (s *Service) postReputationRelease(w http.ResponseWriter, r *http.Request) {
	var input ReputationReleaseRequest
	if !decode(w, r, &input) {
		return
	}
	record, err := s.ReleaseReputation(r.Context(), input)
	respond(w, record, err, http.StatusOK)
}
