package api

import (
	"testing"
)

func TestProjectMigrationContextModes(t *testing.T) {
	plain := ContextWithProjectReleaseMigration(t.Context())
	if projectSafeReleaseMode(plain) != "atomic-migrate-verify" {
		t.Fatal("missing migration opt-in")
	}
	raw := []byte(`{"env":{"web":{"MODE":"new"}}}`)
	paired := ContextWithProjectReleaseMigration(ContextWithProjectReleaseConfig(t.Context(), raw))
	if projectSafeReleaseMode(paired) != "atomic-config-migrate-verify" || string(projectReleaseConfigFromContext(paired)) != string(raw) {
		t.Fatal("lost paired migration configuration")
	}
	command := &ProjectReleaseCommand{AppID: "app", Slug: "web", CommandHash: ProjectReleaseCommandHash([]string{"migrate"}, true), Compatibility: "backward-compatible", Status: "waiting"}
	identity := ProjectReleaseCommandPolicyID(command)
	command.Status = "succeeded"
	command.TaskID = "task"
	exit := 0
	command.ExitCode = &exit
	if ProjectReleaseCommandPolicyID(command) != identity {
		t.Fatal("progress changed command identity")
	}
	command.CommandHash = ProjectReleaseCommandHash([]string{"other"}, true)
	if ProjectReleaseCommandPolicyID(command) == identity {
		t.Fatal("changed command preserved identity")
	}
}

func TestMigrationBaselinePolicyIdentity(t *testing.T) {
	command := &ProjectReleaseCommand{AppID: "app", Slug: "api", CommandHash: ProjectReleaseCommandHash([]string{"expand"}, false), Compatibility: "backward-compatible"}
	legacy := ProjectReleaseCommandPolicyID(command)
	command.VerifyBaseline = true
	guarded := ProjectReleaseCommandPolicyID(command)
	if guarded == legacy {
		t.Fatal("baseline requirement did not bind plan identity")
	}
	command.BaselineVerification = &ProjectMigrationBaselineVerification{TaskID: "task", Status: "healthy"}
	if ProjectReleaseCommandPolicyID(command) != guarded {
		t.Fatal("probe progress changed plan identity")
	}
}

func TestProjectMigrationContractPolicyIdentity(t *testing.T) {
	command := &ProjectReleaseCommand{AppID: "app", Slug: "api", CommandHash: ProjectReleaseCommandHash([]string{"expand"}, true), Compatibility: "backward-compatible", VerifyBaseline: true}
	before := ProjectReleaseCommandPolicyID(command)
	command.ContractCommandHash = ProjectReleaseCommandHash([]string{"contract"}, true)
	identity := ProjectReleaseCommandPolicyID(command)
	if before == identity {
		t.Fatal("cleanup command did not bind source plan")
	}
	command.Contract = &ProjectMigrationContract{Status: "running", TaskID: "task", RequestID: "request", RetiredDeployments: 2}
	if ProjectReleaseCommandPolicyID(command) != identity {
		t.Fatal("contract progress changed immutable plan")
	}
}
