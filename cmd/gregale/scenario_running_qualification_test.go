// adr: 20261018000100
package main_test

import (
	"github.com/onebox-faas/faas/cmd/gregale"
	"testing"
)

func TestRunningJobNativeQualificationFixture(t *testing.T) {
	scenarios, dir, err := main.ReadTestManifest("../../tests/scenario-acceptance/rehearsal-running-job/gregale-test.yaml")
	if err != nil {
		t.Fatal(err)
	}
	s := scenarios["running-import"]
	if err = main.ValidateTestCommandExecution(s, "real-vm", "warm"); err != nil {
		t.Fatal(err)
	}
	files, err := main.PrepareTestCommandFiles(dir, s)
	if err != nil || len(files) != 2 {
		t.Fatalf("contained inputs: %v (%d files)", err, len(files))
	}
	r := main.TestRunReceipt{}
	if _, err = main.PrepareTestPostgres(dir, s, &r); err != nil {
		t.Fatal(err)
	}
	if _, err = main.PrepareTestObjects(dir, s, &r); err != nil {
		t.Fatal(err)
	}
	if main.EstimateTestWorkloadMinutes(s, 2) > 120 {
		t.Fatal("native runner's budget does not admit both revisions")
	}
	for _, engine := range []string{"local", "simulated"} {
		if main.ValidateTestCommandExecution(s, engine, "warm") == nil {
			t.Fatalf("native fixture admitted unsupported engine %s", engine)
		}
	}
	for _, profile := range []string{"cold", "restored"} {
		if main.ValidateTestCommandExecution(s, "real-vm", profile) == nil {
			t.Fatalf("running-job fixture admitted unsupported profile %s", profile)
		}
	}
}
