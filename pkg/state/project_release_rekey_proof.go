package state

import (
	"bytes"
	"encoding/json"
	"sort"

	"github.com/onebox-faas/faas/pkg/api"
)

// Public proofs stay valid across envelope maintenance, including receipts
// issued before rekey support existed. Reuse is allowed only after comparing
// every input of EnvironmentHash with ciphertext and recipient omitted.
// Version counters, timestamps, value HMACs and ownership still fence mutations.
func (current *projectReleaseConfigCapture) reuseSecretEnvironmentProof(prior projectReleaseConfigCapture, descriptor *api.ProjectReleaseConfiguration) {
	if descriptor == nil || prior.Descriptor == nil || prior.Descriptor.Secrets == nil {
		return
	}
	identity := func(c projectReleaseConfigCapture) []byte {
		proof := []any{}
		workloads := append([]projectReleaseConfigWorkload(nil), c.Workloads...)
		sort.Slice(workloads, func(i, j int) bool { return workloads[i].AppID < workloads[j].AppID })
		for _, w := range workloads {
			secrets := append([]runtimeAppSealedSecretRow(nil), w.LiveSecrets...)
			for i := range secrets {
				secrets[i].Kid = ""
				secrets[i].Ciphertext = nil
				secrets[i].CreatedAt = secrets[i].CreatedAt.UTC()
				secrets[i].UpdatedAt = secrets[i].UpdatedAt.UTC()
			}
			sort.Slice(secrets, func(i, j int) bool { return secrets[i].Key < secrets[j].Key })
			proof = append(proof, struct {
				AppID, Name    string
				Variables      []projectReleaseVariable
				OtherVariables int
				SecretKeys     []string
				Secrets        []runtimeAppSealedSecretRow
				Ineligible     bool
			}{w.AppID, w.Name, w.Legacy, w.OtherVariables, w.SecretKeys, secrets, w.SecretIneligible})
		}
		raw, _ := json.Marshal(proof)
		return raw
	}
	if bytes.Equal(identity(*current), identity(prior)) {
		descriptor.EnvironmentHash = prior.Descriptor.EnvironmentHash
	}
}
