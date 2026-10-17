package internalmail

import (
	"net/http"
	"net/url"
	"strconv"

	"github.com/onebox-faas/faas/pkg/api"
)

func (s *Service) registerRecovery(mux *http.ServeMux) {
	mux.HandleFunc("GET "+RecoveryPath, s.getRecovery)
	mux.HandleFunc("GET "+RecoveryPath+"/preview", s.getRecoveryPreview)
	mux.HandleFunc("POST "+RecoveryPath+"/recipients/{id}", s.postRecoveryReview)
	mux.HandleFunc("POST "+RecoveryPath+"/release", s.postRecoveryRelease)
}

func (s *Service) getRecovery(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" {
		respond(w, nil, ErrInvalid, 0)
		return
	}
	status, err := s.RecoveryStatus(r.Context())
	respond(w, status, err, http.StatusOK)
}

func recoveryList(raw string) (OperatorList, error) {
	list := OperatorList{Limit: api.InternalMailOperatorPageSize}
	values, err := url.ParseQuery(raw)
	if err != nil {
		return list, ErrInvalid
	}
	for key, values := range values {
		if len(values) != 1 {
			return list, ErrInvalid
		}
		switch key {
		case "limit":
			list.Limit, err = strconv.Atoi(values[0])
		case "after":
			list.After = values[0]
		default:
			return list, ErrInvalid
		}
		if err != nil {
			return list, ErrInvalid
		}
	}
	return list, list.validate(true)
}

func (s *Service) getRecoveryPreview(w http.ResponseWriter, r *http.Request) {
	list, err := recoveryList(r.URL.RawQuery)
	if err != nil {
		respond(w, nil, err, 0)
		return
	}
	result, err := s.PreviewRecovery(r.Context(), list)
	respond(w, result, err, http.StatusOK)
}

func (s *Service) postRecoveryReview(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" {
		respond(w, nil, ErrInvalid, 0)
		return
	}
	var input RecoveryReviewRequest
	if !decode(w, r, &input) {
		return
	}
	record, err := s.ReviewRecovery(r.Context(), r.PathValue("id"), input)
	respond(w, record, err, http.StatusOK)
}

func (s *Service) postRecoveryRelease(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" {
		respond(w, nil, ErrInvalid, 0)
		return
	}
	var input RecoveryReleaseRequest
	if !decode(w, r, &input) {
		return
	}
	record, err := s.ReleaseRecovery(r.Context(), input)
	respond(w, record, err, http.StatusOK)
}
