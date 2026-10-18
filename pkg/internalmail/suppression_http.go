package internalmail

import (
	"net/http"
	"net/url"
	"strconv"

	"github.com/onebox-faas/faas/pkg/api"
)

func (s *Service) registerSuppressionControls(mux *http.ServeMux) {
	mux.HandleFunc("GET "+SuppressionControlsPath+"/status", s.getSuppressionStatus)
	mux.HandleFunc("GET "+SuppressionControlsPath+"/history", s.getSuppressionHistory)
	mux.HandleFunc("POST "+SuppressionControlsPath+"/{action}", s.postSuppressionControl)
}

func suppressionQuery(r *http.Request, history bool) (string, OperatorList, error) {
	list := OperatorList{Limit: api.InternalMailOperatorPageSize}
	// ParseQuery reports malformed escapes rather than silently dropping them.
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return "", list, ErrInvalid
	}
	for name, values := range values {
		if len(values) != 1 {
			return "", list, ErrInvalid
		}
		switch name {
		case "address":
		case "limit":
			if !history {
				return "", list, ErrInvalid
			}
			list.Limit, err = strconv.Atoi(values[0])
		case "after":
			if !history {
				return "", list, ErrInvalid
			}
			list.After = values[0]
		default:
			return "", list, ErrInvalid
		}
		if err != nil {
			return "", list, ErrInvalid
		}
	}
	if _, err := suppressionHistoryList(list); err != nil {
		return "", list, err
	}
	address, err := NormalizeSuppressionAddress(values.Get("address"))
	return address, list, err
}

func (s *Service) getSuppressionStatus(w http.ResponseWriter, r *http.Request) {
	address, _, err := suppressionQuery(r, false)
	if err != nil {
		respond(w, nil, err, 0)
		return
	}
	status, err := s.SuppressionStatus(r.Context(), address)
	respond(w, status, err, http.StatusOK)
}

func (s *Service) getSuppressionHistory(w http.ResponseWriter, r *http.Request) {
	address, list, err := suppressionQuery(r, true)
	if err != nil {
		respond(w, nil, err, 0)
		return
	}
	page, err := s.SuppressionHistory(r.Context(), address, list)
	respond(w, page, err, http.StatusOK)
}

func (s *Service) postSuppressionControl(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" {
		respond(w, nil, ErrInvalid, 0)
		return
	}
	var input SuppressionRequest
	if !decode(w, r, &input) {
		return
	}
	record, err := s.ChangeSuppression(r.Context(), r.PathValue("action"), input)
	respond(w, record, err, http.StatusOK)
}
