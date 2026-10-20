package state_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/onebox-faas/faas/pkg/state"
	"testing"
	"time"
)

// Archived pre-activation native receipts must retain their original digests.
func TestCheckpointPgActivationRetirementDigestCompatibility(t *testing.T) {
	type oldPlan struct {
		Credential                                                       state.ProjectEnvironmentCloneCheckpointPostgresCredentialPlan
		PreparationState, CiphertextSHA256, PreparationObservationSHA256 string
		PreparationRequestedAt                                           time.Time
	}
	type oldObservation struct {
		RequestID, PreparationRequestID, PlanSHA256, ProviderIdentityID, OwnerID, DataResourceID string
		PreparationFenced, IdentityAbsent, SessionsClosed, NoSourceAuthority, SQLScopeUnchanged  bool
		RetiredAt                                                                                time.Time
	}
	p := state.ProjectEnvironmentCloneCheckpointPostgresCredentialRetirementPlan{PreparationState: "prepared", CiphertextSHA256: "original-envelope", PreparationRequestedAt: time.Unix(1, 0).UTC()}
	raw, err := json.Marshal(oldPlan{Credential: p.Credential, PreparationState: p.PreparationState, CiphertextSHA256: p.CiphertextSHA256, PreparationObservationSHA256: p.PreparationObservationSHA256, PreparationRequestedAt: p.PreparationRequestedAt})
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	if state.CloneCheckpointSHA(p) != hex.EncodeToString(sum[:]) {
		t.Fatal("pre-activation retirement plan digest changed")
	}
	o := state.ProjectEnvironmentCloneCheckpointPostgresCredentialRetirementObservation{RequestID: "original-retirement", PreparationFenced: true, IdentityAbsent: true, SessionsClosed: true, NoSourceAuthority: true, SQLScopeUnchanged: true, RetiredAt: time.Unix(2, 0).UTC()}
	raw, err = json.Marshal(oldObservation{RequestID: o.RequestID, PreparationRequestID: o.PreparationRequestID, PlanSHA256: o.PlanSHA256, ProviderIdentityID: o.ProviderIdentityID, OwnerID: o.OwnerID, DataResourceID: o.DataResourceID, PreparationFenced: o.PreparationFenced, IdentityAbsent: o.IdentityAbsent, SessionsClosed: o.SessionsClosed, NoSourceAuthority: o.NoSourceAuthority, SQLScopeUnchanged: o.SQLScopeUnchanged, RetiredAt: o.RetiredAt})
	if err != nil {
		t.Fatal(err)
	}
	sum = sha256.Sum256(raw)
	if state.CloneCheckpointSHA(o) != hex.EncodeToString(sum[:]) {
		t.Fatal("pre-activation retirement receipt digest changed")
	}
}
