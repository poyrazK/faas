package state

import (
	"encoding/json"
	"errors"
	"math/rand/v2"
	"reflect"
	"strings"
	"testing"
)

func checkpointPgBindingGraphFixture(t *testing.T) (ProjectEnvironmentCloneOperation, []ProjectEnvironmentCloneBindings, ProjectEnvironmentCloneCheckpointPgActivation, ProjectEnvironmentCloneCheckpointPgPublication) {
	t.Helper()
	op, a, p := checkpointPgWorkloadFixture(t)
	base := p.Plan.Secrets[0]
	a.Plan.Bindings, p.Plan.Secrets = nil, nil
	source := ProjectEnvironmentClonePostgresBinding{ID: "binding-a", DatabaseID: "primary", DatabaseName: "primary", EnvironmentKey: "DATABASE_URL", Access: "read_write", CredentialRef: "original-a", CredentialGeneration: 3, BackendID: "native", BackendFingerprint: strings.Repeat("a", 64), ProviderResourceID: "source-owner-primary", DataResourceID: "source-data-primary", Region: "eu-1", PostgresMajor: 16, ServiceClass: "production", Availability: "high_availability", StorageLimitBytes: 4096, RestoreWindowSeconds: 86400}
	secondary := source
	secondary.ID, secondary.DatabaseID, secondary.DatabaseName, secondary.EnvironmentKey, secondary.CredentialRef = "binding-c", "analytics", "analytics", "ANALYTICS_URL", "original-c"
	secondary.ProviderResourceID, secondary.DataResourceID = "source-owner-analytics", "source-data-analytics"
	reader := source
	reader.ID, reader.Access, reader.CredentialRef = "binding-b", "read_only", "original-b"
	views := []ProjectEnvironmentCloneBindings{{AppID: "writer", SourceScope: "production", ProjectEnvironmentCloneBindingDefinitions: ProjectEnvironmentCloneBindingDefinitions{Postgres: []ProjectEnvironmentClonePostgresBinding{source, secondary}}}, {AppID: "reader", SourceScope: "default", ProjectEnvironmentCloneBindingDefinitions: ProjectEnvironmentCloneBindingDefinitions{Postgres: []ProjectEnvironmentClonePostgresBinding{reader}}}}
	for i := range views {
		views[i].Hash, _ = cloneBindingDefinitionsHash(views[i].ProjectEnvironmentCloneBindingDefinitions)
		for _, s := range views[i].Postgres {
			c := ProjectEnvironmentCloneCheckpointPostgresCredentialPlan{SourceBindingID: s.ID, TargetBindingID: "fresh-" + s.ID, ProviderIdentityID: "stage-fresh-" + s.ID, AppID: views[i].AppID, Scope: op.TargetEnvironment, EnvironmentKey: s.EnvironmentKey, Access: s.Access, CredentialGeneration: 1, DataResourceID: "private-data-" + s.DatabaseID}
			c.Runtime.Spec = checkpointPostgresRuntimeSpec(s)
			c.Runtime.Verification.Isolation = ProjectEnvironmentCloneCheckpointPostgresIsolationPlan{OperationID: op.ID, AccountID: op.AccountID, SourceDatabaseID: s.DatabaseID, OwnerID: "private-owner-" + s.DatabaseID, NativeResourceID: "private-native-" + s.DatabaseID, BackendID: s.BackendID, BackendFingerprint: s.BackendFingerprint, SourceDataResourceID: s.DataResourceID}
			a.Plan.Bindings = append(a.Plan.Bindings, ProjectEnvironmentCloneCheckpointPgActivationBinding{Connection: ProjectEnvironmentCloneCheckpointPgConnectionPlan{Credential: c}})
			p.Plan.Secrets = append(p.Plan.Secrets, base)
		}
	}
	resignCheckpointPgBindingGraphFixture(a, &p)
	return op, views, a, p
}

func resignCheckpointPgBindingGraphFixture(a ProjectEnvironmentCloneCheckpointPgActivation, p *ProjectEnvironmentCloneCheckpointPgPublication) {
	for i, member := range a.Plan.Bindings {
		c := member.Connection.Credential
		s := &p.Plan.Secrets[i]
		s.AppID, s.Scope, s.Key, s.ManagedPostgresBindingID, s.ManagedPostgresAccess = c.AppID, c.Scope, c.EnvironmentKey, c.TargetBindingID, c.Access
		s.ManagedCredentialRef = clonePostgresCredentialRef(c.TargetBindingID, 1)
	}
	p.Plan.ActivationPlanSHA256 = cloneCheckpointSHA(a.Plan)
	p.PlanSHA256 = cloneCheckpointSHA(p.Plan)
}

func TestCheckpointPgBindingGraphPreservesSharedDatabaseAndCanonicalOrder(t *testing.T) {
	op, views, a, p := checkpointPgBindingGraphFixture(t)
	preparations, err := checkpointPgWorkloadPreparations(op, a, p)
	if err != nil {
		t.Fatal(err)
	}
	original, _ := json.Marshal(views)
	graph, err := checkpointPgBindingGraph(op, views, a, p, preparations)
	if err != nil || len(graph.Bindings) != 3 || graph.SHA256 != cloneCheckpointSHA(graph) {
		t.Fatal("complete binding graph", err)
	}
	if graph.Bindings[0].Target.DatabaseID != graph.Bindings[1].Target.DatabaseID || graph.Bindings[1].Target.DatabaseID == graph.Bindings[2].Target.DatabaseID || graph.Bindings[0].Target.ID == graph.Bindings[1].Target.ID || graph.Bindings[1].Target.Access != "read_only" {
		t.Fatal("shared database or distinct identity relationship changed")
	}
	for _, edge := range graph.Bindings {
		if edge.Target.DatabaseID == edge.SourceDatabaseID || edge.Target.ID == edge.SourceBindingID || edge.Target.CredentialGeneration != 1 || edge.Target.DatabaseName != ProjectEnvironmentClonePostgresSnapshotRestoreDatabaseName(op, edge.SourceDatabaseID) || edge.Target.CredentialRef != clonePostgresCredentialRef(edge.Target.ID, 1) || edge.Target.ServiceClass != "production" || edge.Target.Availability != "high_availability" {
			t.Fatal("target binding did not preserve original graph")
		}
	}
	rng := rand.New(rand.NewPCG(590, 1))
	for range 20 {
		rng.Shuffle(len(views), func(i, j int) { views[i], views[j] = views[j], views[i] })
		got, err := checkpointPgBindingGraph(op, views, a, p, preparations)
		if err != nil || !reflect.DeepEqual(got, graph) {
			t.Fatal("catalogue order changed graph or digest", err)
		}
	}
	graph.Bindings[0].Target.DatabaseName = "changed"
	if graph.SHA256 == cloneCheckpointSHA(graph) {
		t.Fatal("graph digest did not bind target definitions")
	}
	var restored []ProjectEnvironmentCloneBindings
	if err := json.Unmarshal(original, &restored); err != nil {
		t.Fatal(err)
	}
	// Permutations above changed only app order; projection must not change definitions.
	for _, before := range restored {
		for _, after := range views {
			if before.AppID == after.AppID && !reflect.DeepEqual(before, after) {
				t.Fatal("graph aliased frozen definitions")
			}
		}
	}
}

func TestCheckpointPgBindingGraphRejectsChangedResourceRelationships(t *testing.T) {
	for _, kind := range []string{"source-hash", "missing", "extra", "duplicate-app", "duplicate-source", "source-scope", "source-spec", "source-backend", "source-data", "shared-source-name", "shared-target-split", "different-sources-merged", "native-reuse", "data-reuse", "source-owner", "source-binding", "source-resource", "duplicate-target", "prepared-envelope", "prepared-hash"} {
		t.Run(kind, func(t *testing.T) {
			op, views, a, p := checkpointPgBindingGraphFixture(t)
			switch kind {
			case "source-hash":
				views[0].Hash = "changed"
			case "missing":
				views[0].Postgres = views[0].Postgres[:1]
			case "extra":
				s := views[0].Postgres[0]
				s.ID = "extra"
				views[0].Postgres = append(views[0].Postgres, s)
			case "duplicate-app":
				views = append(views, views[0])
			case "duplicate-source":
				views[1].Postgres[0].ID = views[0].Postgres[0].ID
			case "source-scope":
				views[0].SourceScope = ""
			case "source-spec":
				views[0].Postgres[1].PostgresMajor++
			case "source-backend":
				views[0].Postgres[1].BackendID = "foreign"
			case "source-data":
				views[0].Postgres[1].DataResourceID = "foreign"
			case "shared-source-name":
				views[1].Postgres[0].DatabaseName = "changed"
			case "shared-target-split":
				a.Plan.Bindings[2].Connection.Credential.Runtime.Verification.Isolation.OwnerID = "split-owner"
			case "different-sources-merged":
				a.Plan.Bindings[1].Connection.Credential.Runtime.Verification.Isolation.OwnerID = a.Plan.Bindings[0].Connection.Credential.Runtime.Verification.Isolation.OwnerID
			case "native-reuse":
				a.Plan.Bindings[1].Connection.Credential.Runtime.Verification.Isolation.NativeResourceID = a.Plan.Bindings[0].Connection.Credential.Runtime.Verification.Isolation.NativeResourceID
			case "data-reuse":
				a.Plan.Bindings[1].Connection.Credential.DataResourceID = a.Plan.Bindings[0].Connection.Credential.DataResourceID
			case "source-owner":
				a.Plan.Bindings[1].Connection.Credential.Runtime.Verification.Isolation.OwnerID = "primary"
			case "source-binding":
				a.Plan.Bindings[1].Connection.Credential.TargetBindingID = "binding-a"
			case "source-resource":
				a.Plan.Bindings[1].Connection.Credential.Runtime.Verification.Isolation.NativeResourceID = "source-owner-primary"
			case "duplicate-target":
				a.Plan.Bindings[1].Connection.Credential.TargetBindingID = a.Plan.Bindings[0].Connection.Credential.TargetBindingID
			}
			if kind != "source-hash" {
				for i := range views {
					views[i].Hash, _ = cloneBindingDefinitionsHash(views[i].ProjectEnvironmentCloneBindingDefinitions)
				}
			}
			resignCheckpointPgBindingGraphFixture(a, &p)
			preparations, err := checkpointPgWorkloadPreparations(op, a, p)
			if err != nil {
				t.Fatal("fixture preparation", err)
			}
			if kind == "prepared-envelope" {
				in := preparations["binding-a"]
				in.Secret.Ciphertext = []byte("replacement")
				in, _, _ = normalizeClonePostgresPreparation(in)
				preparations["binding-a"] = in
			}
			if kind == "prepared-hash" {
				in := preparations["binding-a"]
				in.Hash = "changed"
				preparations["binding-a"] = in
			}
			got, err := checkpointPgBindingGraph(op, views, a, p, preparations)
			if !errors.Is(err, ErrProjectEnvironmentCloneManagedValueProof) || !reflect.DeepEqual(got, ProjectEnvironmentCloneCheckpointPgBindingGraph{}) {
				t.Fatal("changed graph accepted", err)
			}
		})
	}
}

func TestCheckpointPgBindingGraphScopesOpaqueResourceIdentities(t *testing.T) {
	for _, kind := range []string{"backend", "fingerprint", "resource-kind"} {
		t.Run(kind, func(t *testing.T) {
			op, views, a, p := checkpointPgBindingGraphFixture(t)
			c := &a.Plan.Bindings[1].Connection.Credential
			if kind == "backend" {
				views[0].Postgres[1].BackendID = "another"
				c.Runtime.Verification.Isolation.BackendID = "another"
			} else if kind == "fingerprint" {
				views[0].Postgres[1].BackendFingerprint = strings.Repeat("b", 64)
				c.Runtime.Verification.Isolation.BackendFingerprint = strings.Repeat("b", 64)
			}
			if kind == "resource-kind" {
				c.Runtime.Verification.Isolation.NativeResourceID = a.Plan.Bindings[0].Connection.Credential.DataResourceID
			} else {
				c.Runtime.Verification.Isolation.NativeResourceID = views[0].Postgres[0].ProviderResourceID
				c.DataResourceID = views[0].Postgres[0].DataResourceID
			}
			views[0].Hash, _ = cloneBindingDefinitionsHash(views[0].ProjectEnvironmentCloneBindingDefinitions)
			resignCheckpointPgBindingGraphFixture(a, &p)
			preparations, err := checkpointPgWorkloadPreparations(op, a, p)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := checkpointPgBindingGraph(op, views, a, p, preparations); err != nil {
				t.Fatal("opaque resource identity crossed its namespace", err)
			}
		})
	}
}
