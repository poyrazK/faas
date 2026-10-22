package state

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func checkpointObjectValueFixture(t *testing.T) (ProjectEnvironmentCloneCheckpointObjectCredentialReservation, ProjectEnvironmentCloneCheckpointObjectCredentialValues) {
	t.Helper()
	op, views, proofs := checkpointObjectCredentialInputFixture(t)
	op.ID, op.AccountID, op.ProjectID = uuid.NewString(), uuid.NewString(), uuid.NewString()
	for i := range views {
		app := uuid.NewString()
		views[i].AppID = app
		b := &views[i].Buckets[0]
		b.ID = uuid.NewString()
		for j := range b.Credentials {
			b.Credentials[j].ID = uuid.NewString()
			if b.Credentials[j].ManagedAppID != "" {
				b.Credentials[j].ManagedAppID = app
			}
		}
		views[i].Hash, _ = cloneBindingDefinitionsHash(views[i].ProjectEnvironmentCloneBindingDefinitions)
		proofs[i].Plan.Target.OperationID = op.ID
		proofs[i].Plan.Target.AccountID = op.AccountID
		proofs[i].Plan.Target.AppID = app
		proofs[i].Plan.Target.SourceBucketID = b.ID
		proofs[i].Plan.Target.TargetBucketID = uuid.NewString()
	}
	inputs, err := checkpointObjectCredentialInputs(op, views, proofs)
	if err != nil {
		t.Fatal(err)
	}
	r, err := newCheckpointObjectCredentialReservation(inputs)
	if err != nil {
		t.Fatal(err)
	}
	out := ProjectEnvironmentCloneCheckpointObjectCredentialValues{OperationID: op.ID, ReservationSHA256: r.SHA256, Endpoint: "https://s3.example.test", Region: "eu", Preparations: []ProjectEnvironmentCloneObjectCredentialPreparation{}}
	n := 0
	for _, b := range r.Inputs.Buckets {
		for _, slot := range b.Credentials {
			access := "GRGA" + strings.Repeat(string(rune('A'+n)), 16)
			c := ObjectS3Credential{ID: r.Members[n].TargetCredentialID, AccountID: op.AccountID, BucketID: b.Target.TargetBucketID, AccessKeyID: access, SecretSealed: []byte("opaque"), KID: "kid", Label: slot.Label, Permission: slot.Permission, Status: ObjectS3CredentialStatusActive, ManagedAppID: slot.ManagedAppID, ManagedScope: slot.TargetScope, ManagedPrefix: slot.ManagedPrefix}
			n++
			p := ProjectEnvironmentCloneObjectCredentialPreparation{OperationID: op.ID, AppID: b.AppID, SourceBucketID: b.Source.ID, SourceCredentialID: slot.SourceCredentialID, Credential: c, Secrets: []AppSecret{}}
			for _, key := range slot.SecretKeys {
				p.Secrets = append(p.Secrets, AppSecret{AccountID: op.AccountID, AppID: b.AppID, Scope: slot.TargetScope, Key: key, Ciphertext: []byte("opaque"), Kid: "kid", ValueHash: "hash", SecretClass: SecretClassPersistent, SecretVersion: 1, ManagedObjectStorageCredentialID: c.ID})
			}
			out.Preparations = append(out.Preparations, p)
		}
	}
	out.SHA256 = cloneCheckpointSHA(out)
	return r, out
}
func TestCheckpointObjectCredentialValuesCompleteAndTampering(t *testing.T) {
	r, in := checkpointObjectValueFixture(t)
	if err := validateCheckpointObjectCredentialValues(in, r); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var recovered ProjectEnvironmentCloneCheckpointObjectCredentialValues
	if err = json.Unmarshal(encoded, &recovered); err != nil {
		t.Fatal(err)
	}
	recovered.SHA256 = in.SHA256
	if err = validateCheckpointObjectCredentialValues(recovered, r); err != nil {
		t.Fatal("round trip", err)
	}
	for name, edit := range map[string]func(*ProjectEnvironmentCloneCheckpointObjectCredentialValues){
		"missing": func(p *ProjectEnvironmentCloneCheckpointObjectCredentialValues) { p.Preparations = p.Preparations[:3] },
		"duplicate": func(p *ProjectEnvironmentCloneCheckpointObjectCredentialValues) {
			p.Preparations[1] = p.Preparations[0]
		},
		"permission": func(p *ProjectEnvironmentCloneCheckpointObjectCredentialValues) {
			p.Preparations[0].Credential.Permission = "write"
		},
		"identity": func(p *ProjectEnvironmentCloneCheckpointObjectCredentialValues) {
			p.Preparations[0].Credential.ID = uuid.NewString()
		},
		"signing key": func(p *ProjectEnvironmentCloneCheckpointObjectCredentialValues) {
			p.Preparations[0].Credential.AccessKeyID = "wrong"
		},
		"duplicate signing key": func(p *ProjectEnvironmentCloneCheckpointObjectCredentialValues) {
			p.Preparations[1].Credential.AccessKeyID = p.Preparations[0].Credential.AccessKeyID
		},
		"unsealed": func(p *ProjectEnvironmentCloneCheckpointObjectCredentialValues) {
			p.Preparations[0].Credential.SecretSealed = nil
		},
		"missing secret": func(p *ProjectEnvironmentCloneCheckpointObjectCredentialValues) {
			for i := range p.Preparations {
				if len(p.Preparations[i].Secrets) > 0 {
					p.Preparations[i].Secrets = p.Preparations[i].Secrets[:5]
					return
				}
			}
		},
		"secret owner": func(p *ProjectEnvironmentCloneCheckpointObjectCredentialValues) {
			for i := range p.Preparations {
				if len(p.Preparations[i].Secrets) > 0 {
					p.Preparations[i].Secrets[0].ManagedObjectStorageCredentialID = uuid.NewString()
					return
				}
			}
		},
		"standalone secret": func(p *ProjectEnvironmentCloneCheckpointObjectCredentialValues) {
			for i := range p.Preparations {
				if len(p.Preparations[i].Secrets) == 0 {
					p.Preparations[i].Secrets = []AppSecret{{Key: "EXTRA"}}
					return
				}
			}
		},
		"endpoint": func(p *ProjectEnvironmentCloneCheckpointObjectCredentialValues) {
			p.Endpoint = "https://user:password@example.test"
		},
		"reservation": func(p *ProjectEnvironmentCloneCheckpointObjectCredentialValues) {
			p.ReservationSHA256 = strings.Repeat("a", 64)
		},
	} {
		t.Run(name, func(t *testing.T) {
			r, p := checkpointObjectValueFixture(t)
			edit(&p)
			p.SHA256 = cloneCheckpointSHA(p)
			if err := validateCheckpointObjectCredentialValues(p, r); err == nil {
				t.Fatal("invalid sealed matrix accepted")
			}
		})
	}
}
