package internalmail

import (
	"net/http"
	"net/url"
	"strings"
)

func (s *Service) registerDeliveryControls(mux *http.ServeMux) {
	mux.HandleFunc("GET "+DeliveryControlsPath, s.getDeliveryControl)
	mux.HandleFunc("POST "+DeliveryControlsPath+"/{action}", s.postDeliveryControl)
	mux.HandleFunc("POST "+OperationsPath+"/messages/{id}/cancel", s.postCancelMessage)
	mux.HandleFunc("POST "+OperationsPath+"/recipients/{id}/cancel", s.postCancelRecipient)
}
func (s *Service) controlRouteDisabled(path string) bool {
	control := path == DeliveryControlsPath || strings.HasPrefix(path, DeliveryControlsPath+"/")
	cancel := strings.HasSuffix(path, "/cancel") && (strings.HasPrefix(path, OperationsPath+"/messages/") || strings.HasPrefix(path, OperationsPath+"/recipients/"))
	return (control || cancel) && !s.deliveryControlsEnabled()
}

func controlStatusScope(raw string) (DeliveryScope, error) {
	scope := DeliveryScope{Kind: "namespace"}
	values, err := url.ParseQuery(raw)
	if err != nil {
		return scope, ErrInvalid
	}
	for name, v := range values {
		if name != "domain_id" || len(v) != 1 || !canonicalOperatorID(v[0]) {
			return scope, ErrInvalid
		}
		scope = DeliveryScope{Kind: "domain", DomainID: v[0]}
	}
	return scope, nil
}
func (s *Service) getDeliveryControl(w http.ResponseWriter, r *http.Request) {
	scope, err := controlStatusScope(r.URL.RawQuery)
	if err != nil {
		respond(w, nil, err, 0)
		return
	}
	status, err := s.DeliveryStatus(r.Context(), scope)
	respond(w, status, err, http.StatusOK)
}
func (s *Service) postDeliveryControl(w http.ResponseWriter, r *http.Request) {
	mode := map[string]string{"pause": "paused", "resume": "running", "drain": "draining"}[r.PathValue("action")]
	if mode == "" {
		http.NotFound(w, r)
		return
	}
	var input DeliveryControlRequest
	if !decode(w, r, &input) {
		return
	}
	record, err := s.ChangeDeliveryControl(r.Context(), mode, input)
	respond(w, record, err, http.StatusOK)
}
func (s *Service) postCancellation(w http.ResponseWriter, r *http.Request, message bool) {
	if !validOperatorID(w, r) {
		return
	}
	var input OperatorAuditInput
	if !decode(w, r, &input) {
		return
	}
	record, err := s.CancelMail(r.Context(), r.PathValue("id"), message, input)
	respond(w, record, err, http.StatusOK)
}
func (s *Service) postCancelMessage(w http.ResponseWriter, r *http.Request) {
	s.postCancellation(w, r, true)
}
func (s *Service) postCancelRecipient(w http.ResponseWriter, r *http.Request) {
	s.postCancellation(w, r, false)
}
