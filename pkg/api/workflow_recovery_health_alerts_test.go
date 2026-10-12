package api

import "testing"

func TestRecoveryHealthAlertSettings(t *testing.T) {
	for _, settings := range []WorkflowRecoveryHealthAlertPolicy{{true, 0, 60}, {true, 300, 0}, {false, 300, 86401}, {false, 86401, 60}} {
		if settings.Validate() == nil {
			t.Fatalf("accepted %+v", settings)
		}
	}
	if err := (WorkflowRecoveryHealthAlertPolicy{true, 300, 60}).Validate(); err != nil {
		t.Fatal(err)
	}
}
