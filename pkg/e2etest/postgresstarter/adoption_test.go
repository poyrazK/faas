package postgresstarter

import (
	"encoding/json"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestAdoptionRequiresCurrentWriterVersionsAndProcessGenerations(t *testing.T) {
	generation := int64(4)
	notPending := false
	inventory := api.AppBindingInventory{Complete: true, Bindings: []api.AppBindingInventoryItem{{
		Type: api.BindingTypePostgres, Binding: "DATABASE_URL", CredentialGeneration: &generation, RotationPending: &notPending,
		ApplicationAdoption: &api.BindingApplicationAdoption{Complete: true, Status: "current", SecretsExpected: 1, SecretsObserved: 1,
			Application: api.BindingAdoptionCounts{Current: 1}, Targets: []api.BindingApplicationAckTarget{{
				DeploymentID: "deployment", InstanceID: "instance", Key: "DATABASE_URL", CurrentVersion: 3, ApplicationAckVersion: 3,
				ProcessGeneration: "process", ApplicationAckGeneration: "process"}}},
	}}}
	if !Adopted(inventory, "deployment", generation) {
		t.Fatal("current public writer adoption rejected")
	}
	data, _ := json.Marshal(inventory)
	var two api.AppBindingInventory
	if err := json.Unmarshal(data, &two); err != nil {
		t.Fatal(err)
	}
	adoption := two.Bindings[0].ApplicationAdoption
	adoption.Targets = append(adoption.Targets, adoption.Targets[0])
	adoption.Targets[1].InstanceID = "second-instance"
	adoption.Application.Current = 2
	if !Adopted(two, "deployment", generation) {
		t.Fatal("current ACKs from two authorized instances rejected")
	}
	adoption.Targets[1].InstanceID = "instance"
	if Adopted(two, "deployment", generation) {
		t.Fatal("duplicate resident receipt accepted")
	}
	for _, test := range []struct {
		name   string
		mutate func(*api.AppBindingInventory)
	}{
		{"partial inventory", func(i *api.AppBindingInventory) { i.Complete = false }},
		{"reader only", func(i *api.AppBindingInventory) { i.Bindings[0].Binding = "READ_DATABASE_URL" }},
		{"old credential", func(i *api.AppBindingInventory) { *i.Bindings[0].CredentialGeneration = 3 }},
		{"pending rotation", func(i *api.AppBindingInventory) { *i.Bindings[0].RotationPending = true }},
		{"absent adoption", func(i *api.AppBindingInventory) { i.Bindings[0].ApplicationAdoption = nil }},
		{"incomplete adoption", func(i *api.AppBindingInventory) { i.Bindings[0].ApplicationAdoption.Complete = false }},
		{"failed adoption", func(i *api.AppBindingInventory) { i.Bindings[0].ApplicationAdoption.Status = "failed" }},
		{"no residents", func(i *api.AppBindingInventory) { i.Bindings[0].ApplicationAdoption.SecretsExpected = 0 }},
		{"missing receipt", func(i *api.AppBindingInventory) { i.Bindings[0].ApplicationAdoption.SecretsObserved = 0 }},
		{"noncurrent receipt", func(i *api.AppBindingInventory) { i.Bindings[0].ApplicationAdoption.Application.Current = 0 }},
		{"foreign deployment", func(i *api.AppBindingInventory) { i.Bindings[0].ApplicationAdoption.Targets[0].DeploymentID = "old" }},
		{"stale version", func(i *api.AppBindingInventory) {
			i.Bindings[0].ApplicationAdoption.Targets[0].ApplicationAckVersion = 2
		}},
		{"missing process", func(i *api.AppBindingInventory) { i.Bindings[0].ApplicationAdoption.Targets[0].ProcessGeneration = "" }},
		{"old process", func(i *api.AppBindingInventory) {
			i.Bindings[0].ApplicationAdoption.Targets[0].ApplicationAckGeneration = "old"
		}},
		{"foreign secret", func(i *api.AppBindingInventory) {
			i.Bindings[0].ApplicationAdoption.Targets[0].Key = "MIGRATION_DATABASE_URL"
		}},
		{"missing instance", func(i *api.AppBindingInventory) { i.Bindings[0].ApplicationAdoption.Targets[0].InstanceID = "" }},
		{"wrong workload", func(i *api.AppBindingInventory) { i.Bindings[0].ApplicationAdoption.Targets[0].WorkloadName = "task" }},
		{"duplicate binding", func(i *api.AppBindingInventory) { i.Bindings = append(i.Bindings, i.Bindings[0]) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			data, _ := json.Marshal(inventory)
			var changed api.AppBindingInventory
			if err := json.Unmarshal(data, &changed); err != nil {
				t.Fatal(err)
			}
			test.mutate(&changed)
			if Adopted(changed, "deployment", generation) {
				t.Fatal("stale or incomplete application evidence accepted")
			}
		})
	}
}
