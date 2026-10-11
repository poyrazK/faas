package state

import (
	"encoding/hex"
	"errors"
	"testing"

	"filippo.io/age"
)

func checkpointPgWorkloadFixture(t *testing.T) (ProjectEnvironmentCloneOperation, ProjectEnvironmentCloneCheckpointPgActivation, ProjectEnvironmentCloneCheckpointPgPublication) {
	t.Helper()
	key, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	op := ProjectEnvironmentCloneOperation{ID: "operation", AccountID: "account", ProjectID: "project", TargetEnvironment: "stage", SourceRevisionHash: "revision"}
	c := ProjectEnvironmentCloneCheckpointPostgresCredentialPlan{SourceBindingID: "source", TargetBindingID: "target", ProviderIdentityID: "fresh-identity", AppID: "app", Scope: "stage", EnvironmentKey: "DATABASE_URL", Access: "read_write", CredentialGeneration: 1}
	c.Runtime.Verification.Isolation = ProjectEnvironmentCloneCheckpointPostgresIsolationPlan{OperationID: op.ID, AccountID: op.AccountID, OwnerID: "fork"}
	a := ProjectEnvironmentCloneCheckpointPgActivation{State: "active", ObservationSHA256: "receipt", Plan: ProjectEnvironmentCloneCheckpointPgActivationPlan{OperationID: op.ID, AccountID: op.AccountID, ProjectID: op.ProjectID, Scope: op.TargetEnvironment, SourceRevisionHash: op.SourceRevisionHash, Bindings: []ProjectEnvironmentCloneCheckpointPgActivationBinding{{Connection: ProjectEnvironmentCloneCheckpointPgConnectionPlan{Credential: c}}}}}
	s := AppSecret{AccountID: op.AccountID, AppID: c.AppID, Scope: c.Scope, Key: c.EnvironmentKey, Ciphertext: []byte("original encrypted envelope"), Kid: key.Recipient().String(), ValueHash: "0123456789abcdef", SecretClass: SecretClassPersistent, SecretVersion: 1, ManagedPostgresBindingID: c.TargetBindingID, ManagedPostgresAccess: c.Access, ManagedCredentialRef: clonePostgresCredentialRef(c.TargetBindingID, 1), ManagedCredentialGeneration: 1}
	p := ProjectEnvironmentCloneCheckpointPgPublication{State: "published", Plan: ProjectEnvironmentCloneCheckpointPgPublicationPlan{OperationID: op.ID, ActivationPlanSHA256: cloneCheckpointSHA(a.Plan), ActivationObservationSHA256: a.ObservationSHA256, Secrets: []AppSecret{s}}}
	p.PlanSHA256 = cloneCheckpointSHA(p.Plan)
	return op, a, p
}

func TestCheckpointPgWorkloadPreparationsUseCommittedEnvelope(t *testing.T) {
	op, a, p := checkpointPgWorkloadFixture(t)
	preparations, err := checkpointPgWorkloadPreparations(op, a, p)
	if err != nil {
		t.Fatal(err)
	}
	source := projectCloneWorkloadValues{Variables: []projectCloneVariable{{AppID: "app", Scope: "source", Key: "MODE", Value: "captured"}}, Secrets: []projectCloneSecret{{AppID: "app", Scope: "source", Key: "DATABASE_URL", ManagedPostgresBindingID: "source", ManagedCredentialRef: "old-reference"}}}
	record := projectCloneWorkloadRecord{ProjectEnvironmentCloneWorkload: ProjectEnvironmentCloneWorkload{AppID: "app"}}
	expected, err := expectedCloneWorkloadTargetValues(op, record, source, projectCloneManagedValuePreparations{Postgres: preparations})
	if err != nil || expected.Variables[0].Scope != "stage" || expected.Variables[0].Value != "captured" || expected.Secrets[0].Ciphertext != "\\x"+hex.EncodeToString(p.Plan.Secrets[0].Ciphertext) || expected.Secrets[0].ManagedPostgresBindingID != "target" {
		t.Fatal("workload values did not use original publication", err)
	}
	preparations["source"].Secret.Ciphertext[0] ^= 1
	if string(p.Plan.Secrets[0].Ciphertext) != "original encrypted envelope" {
		t.Fatal("projection aliases journal ciphertext")
	}
}

func TestCheckpointPgWorkloadPreparationsRejectChangedOwnership(t *testing.T) {
	for _, kind := range []string{"withdrawn", "inactive", "partial", "journal-digest", "activation-digest", "receipt", "operation", "account", "scope", "generation", "source-identity", "duplicate"} {
		t.Run(kind, func(t *testing.T) {
			op, a, p := checkpointPgWorkloadFixture(t)
			switch kind {
			case "withdrawn":
				p.State = "withdrawn"
			case "inactive":
				a.State = "requested"
			case "partial":
				p.Plan.Secrets = nil
			case "journal-digest":
				p.PlanSHA256 = "changed"
			case "activation-digest":
				p.Plan.ActivationPlanSHA256 = "changed"
			case "receipt":
				p.Plan.ActivationObservationSHA256 = "changed"
			case "operation":
				op.ID = "foreign"
			case "account":
				op.AccountID = "foreign"
			case "scope":
				a.Plan.Bindings[0].Connection.Credential.Scope = "foreign"
			case "generation":
				a.Plan.Bindings[0].Connection.Credential.CredentialGeneration = 2
			case "source-identity":
				a.Plan.Bindings[0].Connection.Credential.TargetBindingID = "source"
			case "duplicate":
				a.Plan.Bindings = append(a.Plan.Bindings, a.Plan.Bindings[0])
				p.Plan.Secrets = append(p.Plan.Secrets, p.Plan.Secrets[0])
			}
			// Re-sign altered members to exercise ownership checks beyond digest checks.
			if kind == "scope" || kind == "generation" || kind == "source-identity" || kind == "duplicate" {
				p.Plan.ActivationPlanSHA256 = cloneCheckpointSHA(a.Plan)
			}
			if kind != "journal-digest" {
				p.PlanSHA256 = cloneCheckpointSHA(p.Plan)
			}
			if got, err := checkpointPgWorkloadPreparations(op, a, p); !errors.Is(err, ErrProjectEnvironmentCloneManagedValueProof) || got != nil {
				t.Fatal("changed preparation accepted", err)
			}
		})
	}
}

func TestCheckpointPgEmptyWorkloadProjection(t *testing.T) {
	op, views, activation, publication := checkpointPgBindingGraphFixture(t)
	activation.Plan.Bindings, publication.Plan.Secrets = nil, nil
	publication.Plan.ActivationPlanSHA256 = cloneCheckpointSHA(activation.Plan)
	publication.PlanSHA256 = cloneCheckpointSHA(publication.Plan)
	preparations, err := checkpointPgWorkloadPreparations(op, activation, publication)
	if err != nil || preparations == nil || len(preparations) != 0 {
		t.Fatal("empty projection", err)
	}
	if _, err = checkpointPgBindingGraph(op, views, activation, publication, preparations); !errors.Is(err, ErrProjectEnvironmentCloneManagedValueProof) {
		t.Fatal("empty projection erased captured bindings", err)
	}
	for i := range views {
		views[i].Postgres = nil
		views[i].Hash, err = cloneBindingDefinitionsHash(views[i].ProjectEnvironmentCloneBindingDefinitions)
		if err != nil {
			t.Fatal(err)
		}
	}
	graph, err := checkpointPgBindingGraph(op, views, activation, publication, preparations)
	if err != nil || len(graph.Bindings) != 0 || graph.SHA256 != cloneCheckpointSHA(graph) {
		t.Fatal("genuine empty graph", err)
	}
}
