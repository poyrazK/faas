package internalmail

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
)

func (s *Service) registerEncryption(mux *http.ServeMux) {
	mux.HandleFunc("GET "+EncryptionPath, s.getEncryption)
	mux.HandleFunc("POST "+EncryptionPath+"/preview", s.postEncryptionPreview)
	mux.HandleFunc("POST "+EncryptionPath+"/{action}", s.postEncryption)
}

func (s *Service) getEncryption(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" {
		respond(w, nil, ErrInvalid, 0)
		return
	}
	status, err := s.EncryptionStatus(r.Context())
	respond(w, status, err, http.StatusOK)
}

func (s *Service) postEncryptionPreview(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" {
		respond(w, nil, ErrInvalid, 0)
		return
	}
	input := struct {
		BatchSize int `json:"batch_size"`
	}{BatchSize: api.InternalMailEncryptionBatchSize}
	if !decode(w, r, &input) {
		return
	}
	result, err := s.PreviewEncryption(r.Context(), input.BatchSize)
	respond(w, result, err, http.StatusOK)
}

func (s *Service) postEncryption(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" {
		respond(w, nil, ErrInvalid, 0)
		return
	}
	var input EncryptionRequest
	if !decode(w, r, &input) {
		return
	}
	record, err := s.ChangeEncryption(r.Context(), r.PathValue("action"), input)
	respond(w, record, err, http.StatusOK)
}
