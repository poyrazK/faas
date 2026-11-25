package dbguest

import "testing"

func TestMigrationVerificationRequiresWritePause(t *testing.T) {
	request := Request{
		Op: OpMigrationVerify,
		Import: &ImportSource{Database: "source", User: "replicator", Password: "fixture",
			TargetDatabase: "target", Owner: "schema_owner", OperationID: "770539f6-8d31-4797-8e18-5fc460d8b886"},
		VerificationID: "a1cedaf7-530e-4867-8510-d3b729d8c592",
	}
	if request.Validate() == nil {
		t.Fatal("verification without a confirmed write pause was accepted")
	}
	request.SourceWritesPaused = true
	if err := request.Validate(); err != nil {
		t.Fatalf("scoped verification rejected: %v", err)
	}
	request.Op = OpMigrationStart
	if request.Validate() == nil {
		t.Fatal("verification options accepted on another command")
	}
}
