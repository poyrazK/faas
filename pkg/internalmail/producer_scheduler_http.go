package internalmail

import "net/http"

func (s *Service) registerProducerScheduler(mux *http.ServeMux) {
	mux.HandleFunc("GET "+ProducerSchedulerPath, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawQuery != "" {
			respond(w, nil, ErrInvalid, 0)
			return
		}
		status, err := s.ProducerDispatchStatus(r.Context())
		respond(w, status, err, http.StatusOK)
	})
}
