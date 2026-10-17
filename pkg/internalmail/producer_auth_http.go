package internalmail

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"net/http"
	"strings"
)

func (s *Service) matchProducerCredential(r *http.Request) (producerAuthentication, error) {
	id, _, ok := strings.Cut(strings.TrimPrefix(r.URL.Path, ProducersPath+"/"), "/")
	p, err := s.producer(id)
	if !ok || err != nil || !validToken(p.Token) {
		return producerAuthentication{}, ErrNotFound
	}
	actual := sha256.Sum256([]byte(r.Header.Get("Authorization")))
	primary := sha256.Sum256([]byte("Bearer " + p.Token))
	replacement := sha256.Sum256([]byte("Bearer " + p.ReplacementToken))
	primaryMatch := subtle.ConstantTimeCompare(actual[:], primary[:])
	replacementMatch := subtle.ConstantTimeCompare(actual[:], replacement[:])
	if len(r.Header.Values("Authorization")) != 1 {
		return producerAuthentication{}, ErrProducerCredential
	}
	auth := producerAuthentication{ProducerID: id, PrimaryID: producerCredentialID(id, p.Token)}
	if primaryMatch == 1 {
		auth.CredentialID = auth.PrimaryID
		return auth, nil
	}
	if replacementMatch != 1 || !validToken(p.ReplacementToken) || !s.producerLifecycleEnabled() {
		return producerAuthentication{}, ErrProducerCredential
	}
	auth.CredentialID = producerCredentialID(id, p.ReplacementToken)
	auth.Enabled = s.producerLifecycleEnabled
	return auth, nil
}

func (s *Service) authenticateProducer(w http.ResponseWriter, r *http.Request) (*http.Request, bool) {
	auth, err := s.matchProducerCredential(r)
	if err == nil {
		if store, ok := s.Store.(producerLifecycleStore); ok {
			err = store.checkProducerAuthentication(r.Context(), auth)
		} else if s.producerLifecycleEnabled() {
			err = ErrInvalid
		}
	}
	if err != nil {
		respond(w, nil, err, 0)
		return r, false
	}
	return r.WithContext(context.WithValue(r.Context(), producerAuthenticationKey{}, auth)), true
}
