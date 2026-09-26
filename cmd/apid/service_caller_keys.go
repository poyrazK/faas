package main

import (
	"encoding/json"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/servicecaller"
	"github.com/onebox-faas/faas/pkg/state"
)

const serviceCallerKeysCacheControl = "public, max-age=5, must-revalidate"

// serviceCallerKeys publishes only the public keys required to verify signed
// service-caller assertions. It intentionally carries no account metadata.
func (s *server) serviceCallerKeys(w http.ResponseWriter, r *http.Request) {
	keyStore, ok := s.store.(state.ServiceCallerKeyStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("service caller verification keys are unavailable"))
		return
	}
	rows, err := keyStore.ListServiceCallerKeys(r.Context())
	if err != nil {
		if s.log != nil {
			s.log.Error("list service caller verification keys", "err", err)
		}
		api.WriteProblem(w, api.ErrInternal("service caller verification keys are unavailable"))
		return
	}
	if len(rows) > servicecaller.MaxJWKSKeys {
		if s.log != nil {
			s.log.Error("service caller verification key set exceeds limit", "count", len(rows), "limit", servicecaller.MaxJWKSKeys)
		}
		api.WriteProblem(w, api.ErrInternal("service caller verification keys are unavailable"))
		return
	}
	keys := make(servicecaller.TrustedKeys, len(rows))
	for _, row := range rows {
		if len(row.PublicKeyPEM) > servicecaller.MaxPublicKeyPEMBytes {
			if s.log != nil {
				s.log.Error("oversized service caller verification key in store", "node_id", row.NodeID, "size", len(row.PublicKeyPEM))
			}
			api.WriteProblem(w, api.ErrInternal("service caller verification keys are unavailable"))
			return
		}
		publicKey, err := servicecaller.ParsePublicKeyPEM([]byte(row.PublicKeyPEM))
		if err != nil || servicecaller.KeyID(publicKey) != row.KeyID {
			if s.log != nil {
				s.log.Error("invalid service caller verification key in store", "node_id", row.NodeID, "err", err)
			}
			api.WriteProblem(w, api.ErrInternal("service caller verification keys are unavailable"))
			return
		}
		if _, exists := keys[row.KeyID]; exists {
			if s.log != nil {
				s.log.Error("duplicate service caller verification key id in store", "node_id", row.NodeID)
			}
			api.WriteProblem(w, api.ErrInternal("service caller verification keys are unavailable"))
			return
		}
		keys[row.KeyID] = publicKey
	}
	set, err := servicecaller.JWKSFromTrustedKeys(keys)
	if err != nil {
		if s.log != nil {
			s.log.Error("build service caller key set", "err", err)
		}
		api.WriteProblem(w, api.ErrInternal("service caller verification keys are unavailable"))
		return
	}
	response := api.ServiceCallerJWKSet{Keys: make([]api.ServiceCallerJWK, 0, len(set.Keys))}
	for _, key := range set.Keys {
		response.Keys = append(response.Keys, api.ServiceCallerJWK{
			Kty: key.Kty,
			Crv: key.Crv,
			Kid: key.Kid,
			X:   key.X,
			Alg: key.Alg,
			Use: key.Use,
		})
	}
	w.Header().Set("Content-Type", "application/jwk-set+json")
	w.Header().Set("Cache-Control", serviceCallerKeysCacheControl)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if err := json.NewEncoder(w).Encode(response); err != nil && s.log != nil {
		s.log.Error("encode service caller key set", "err", err)
	}
}
