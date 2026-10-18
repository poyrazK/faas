// adr: 20261018000100
package main

import (
	"testing"
)

func TestRunningJobNativeQualificationFixture(t *testing.T) {
	scenarios, dir, err := readTestManifest("../../tests/scenario-acceptance/rehearsal-running-job/gregale-test.yaml")
	if err != nil {
		t.Fatal(err)
	}
	s := scenarios["running-import"]
	if err = validateTestCommandExecution(s, "real-vm", "warm"); err != nil {
		t.Fatal(err)
	}
	files, err := prepareTestCommandFiles(dir, s)
	if err != nil || len(files) != 2 {
		t.Fatalf("contained inputs: %v (%d files)", err, len(files))
	}
	r := testRunReceipt{}
	if _, err = prepareTestPostgres(dir, s, &r); err != nil {
		t.Fatal(err)
	}
	if _, err = prepareTestObjects(dir, s, &r); err != nil {
		t.Fatal(err)
	}
	if estimateTestWorkloadMinutes(s, 2) > 120 {
		t.Fatal("native runner's budget does not admit both revisions")
	}
	for _, engine := range []string{"local", "simulated"} {
		if validateTestCommandExecution(s, engine, "warm") == nil {
			t.Fatalf("native fixture admitted unsupported engine %s", engine)
		}
	}
	for _, profile := range []string{"cold", "restored"} {
		if validateTestCommandExecution(s, "real-vm", profile) == nil {
			t.Fatalf("running-job fixture admitted unsupported profile %s", profile)
		}
	}
}
