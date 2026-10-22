package state

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

func checkpointObjectCredentialInputFixture(t *testing.T) (ProjectEnvironmentCloneOperation, []ProjectEnvironmentCloneBindings, []ProjectEnvironmentCloneCheckpointObjectVerification) {
	t.Helper()
	op := ProjectEnvironmentCloneOperation{ID: "operation", AccountID: "account", ProjectID: "project", TargetEnvironment: "stage", SourceRevisionHash: strings.Repeat("a", 64)}
	views := []ProjectEnvironmentCloneBindings{}
	proofs := []ProjectEnvironmentCloneCheckpointObjectVerification{}
	for _, app := range []string{"writer", "reader"} {
		b := ProjectEnvironmentCloneObjectBucket{ID: "source-" + app, Name: "files", Region: "eu", BackendID: "native", BackendFingerprint: strings.Repeat("b", 64), PhysicalName: "source-native-" + app, Credentials: []ProjectEnvironmentCloneObjectCredential{{ID: "credential-" + app, Label: "files", Permission: "read_write", ManagedAppID: app, ManagedScope: "production", ManagedPrefix: "FILES"}, {ID: "external-" + app, Label: "backup", Permission: ObjectBucketPermissionRead}}, AccessGrants: []ProjectEnvironmentCloneObjectAccessGrant{{APIKeyID: "account-key", Permission: ObjectBucketPermissionRead}}}
		v := ProjectEnvironmentCloneBindings{AppID: app, SourceScope: "production", ProjectEnvironmentCloneBindingDefinitions: ProjectEnvironmentCloneBindingDefinitions{Buckets: []ProjectEnvironmentCloneObjectBucket{b}}}
		v.Hash, _ = cloneBindingDefinitionsHash(v.ProjectEnvironmentCloneBindingDefinitions)
		views = append(views, v)
		p := ProjectEnvironmentCloneCheckpointObjectVerification{State: "verified", RequestedAt: time.Now().UTC().Truncate(time.Microsecond), ObservationSHA256: strings.Repeat("c", 64), Plan: ProjectEnvironmentCloneCheckpointObjectVerificationPlan{Target: ProjectEnvironmentCloneCheckpointObjectTargetPlan{OperationID: op.ID, AccountID: op.AccountID, AppID: app, SourceBucketID: b.ID, TargetBucketID: "fresh-" + app, BackendID: b.BackendID, BackendFingerprint: b.BackendFingerprint, Region: "eu", PhysicalName: "fresh-native-" + app, CheckpointID: "original", ManifestSHA256: strings.Repeat("d", 64)}, SourceResourceID: b.PhysicalName, CreationSHA256: strings.Repeat("e", 64), CopySHA256: strings.Repeat("f", 64)}}
		proofs = append(proofs, p)
	}
	return op, views, proofs
}
func TestCheckpointObjectCredentialInputsCompleteAndStable(t *testing.T) {
	op, v, p := checkpointObjectCredentialInputFixture(t)
	original, _ := json.Marshal(v)
	got, err := checkpointObjectCredentialInputs(op, v, p)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Buckets) != 2 || got.SHA256 != cloneCheckpointSHA(got) {
		t.Fatal("incomplete graph")
	}
	for _, b := range got.Buckets {
		if len(b.Credentials) != 2 || len(b.Source.AccessGrants) != 1 || b.Source.AccessGrants[0].APIKeyID != "account-key" {
			t.Fatal("grant roster lost")
		}
		for _, c := range b.Credentials {
			if c.ManagedAppID != "" {
				if len(c.SecretKeys) != 6 || c.TargetScope != op.TargetEnvironment || c.ManagedPrefix != "FILES" {
					t.Fatal("managed slots lost")
				}
			} else if len(c.SecretKeys) != 0 || c.TargetScope != "" {
				t.Fatal("unmanaged credential gained secrets")
			}
		}
	}
	v[0], v[1] = v[1], v[0]
	p[0], p[1] = p[1], p[0]
	again, err := checkpointObjectCredentialInputs(op, v, p)
	if err != nil || !reflect.DeepEqual(got, again) {
		t.Fatal("noncanonical input order", err)
	}
	got.Buckets[0].Source.Credentials[0].Label = "changed"
	got.Buckets[0].Source.AccessGrants[0].Permission = "read_write"
	got.Buckets[0].Credentials[0].SecretKeys[0] = "changed"
	v[0], v[1] = v[1], v[0]
	after, _ := json.Marshal(v)
	if string(after) != string(original) {
		t.Fatal("projection mutated frozen catalogue")
	}
}
func TestCheckpointObjectCredentialInputsRejectIncompleteAndAliasedGraph(t *testing.T) {
	for name, edit := range map[string]func(*ProjectEnvironmentCloneOperation, []ProjectEnvironmentCloneBindings, *[]ProjectEnvironmentCloneCheckpointObjectVerification){
		"missing": func(_ *ProjectEnvironmentCloneOperation, _ []ProjectEnvironmentCloneBindings, p *[]ProjectEnvironmentCloneCheckpointObjectVerification) {
			*p = (*p)[:1]
		},
		"extra": func(_ *ProjectEnvironmentCloneOperation, _ []ProjectEnvironmentCloneBindings, p *[]ProjectEnvironmentCloneCheckpointObjectVerification) {
			extra := (*p)[0]
			extra.Plan.Target.SourceBucketID = "other"
			*p = append(*p, extra)
		},
		"duplicate": func(_ *ProjectEnvironmentCloneOperation, _ []ProjectEnvironmentCloneBindings, p *[]ProjectEnvironmentCloneCheckpointObjectVerification) {
			*p = append(*p, (*p)[0])
		},
		"unverified": func(_ *ProjectEnvironmentCloneOperation, _ []ProjectEnvironmentCloneBindings, p *[]ProjectEnvironmentCloneCheckpointObjectVerification) {
			(*p)[0].State = "requested"
		},
		"copyless": func(_ *ProjectEnvironmentCloneOperation, _ []ProjectEnvironmentCloneBindings, p *[]ProjectEnvironmentCloneCheckpointObjectVerification) {
			(*p)[0].Plan.CopySHA256 = ""
		},
		"owner": func(_ *ProjectEnvironmentCloneOperation, _ []ProjectEnvironmentCloneBindings, p *[]ProjectEnvironmentCloneCheckpointObjectVerification) {
			(*p)[0].Plan.Target.OperationID = "other"
		},
		"app": func(_ *ProjectEnvironmentCloneOperation, _ []ProjectEnvironmentCloneBindings, p *[]ProjectEnvironmentCloneCheckpointObjectVerification) {
			(*p)[0].Plan.Target.AppID = "other"
		},
		"source": func(_ *ProjectEnvironmentCloneOperation, _ []ProjectEnvironmentCloneBindings, p *[]ProjectEnvironmentCloneCheckpointObjectVerification) {
			(*p)[0].Plan.SourceResourceID = "live"
		},
		"merged": func(_ *ProjectEnvironmentCloneOperation, _ []ProjectEnvironmentCloneBindings, p *[]ProjectEnvironmentCloneCheckpointObjectVerification) {
			(*p)[1].Plan.Target.TargetBucketID = (*p)[0].Plan.Target.TargetBucketID
		},
		"native-alias": func(_ *ProjectEnvironmentCloneOperation, _ []ProjectEnvironmentCloneBindings, p *[]ProjectEnvironmentCloneCheckpointObjectVerification) {
			(*p)[1].Plan.Target.PhysicalName = (*p)[0].Plan.Target.PhysicalName
		},
		"source-target": func(_ *ProjectEnvironmentCloneOperation, v []ProjectEnvironmentCloneBindings, p *[]ProjectEnvironmentCloneCheckpointObjectVerification) {
			(*p)[0].Plan.Target.PhysicalName = v[1].Buckets[0].PhysicalName
		},
		"hash": func(_ *ProjectEnvironmentCloneOperation, v []ProjectEnvironmentCloneBindings, _ *[]ProjectEnvironmentCloneCheckpointObjectVerification) {
			v[0].Buckets[0].Credentials[0].Permission = ObjectBucketPermissionRead
		},
		"scope": func(_ *ProjectEnvironmentCloneOperation, v []ProjectEnvironmentCloneBindings, _ *[]ProjectEnvironmentCloneCheckpointObjectVerification) {
			v[0].Buckets[0].Credentials[0].ManagedScope = "other"
			v[0].Hash, _ = cloneBindingDefinitionsHash(v[0].ProjectEnvironmentCloneBindingDefinitions)
		},
		"credential-merge": func(_ *ProjectEnvironmentCloneOperation, v []ProjectEnvironmentCloneBindings, _ *[]ProjectEnvironmentCloneCheckpointObjectVerification) {
			v[1].Buckets[0].Credentials[0].ID = v[0].Buckets[0].Credentials[0].ID
			v[1].Hash, _ = cloneBindingDefinitionsHash(v[1].ProjectEnvironmentCloneBindingDefinitions)
		},
	} {
		t.Run(name, func(t *testing.T) {
			op, v, p := checkpointObjectCredentialInputFixture(t)
			edit(&op, v, &p)
			if _, err := checkpointObjectCredentialInputs(op, v, p); err == nil {
				t.Fatal("invalid graph accepted")
			}
		})
	}
}

func TestCheckpointObjectCredentialInputsScopeNativeIdentities(t *testing.T) {
	op, v, p := checkpointObjectCredentialInputFixture(t)
	p[1].Plan.Target.PhysicalName = p[0].Plan.Target.PhysicalName
	p[1].Plan.Target.BackendID = "another-backend"
	if _, err := checkpointObjectCredentialInputs(op, v, p); err != nil {
		t.Fatal("separate backend identity conflated", err)
	}
}
