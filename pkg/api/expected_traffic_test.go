package api

import "testing"

func TestValidateExpectedTraffic(t *testing.T) {
	const a, b = "6f1c1b7e-2f0a-4f43-9d2b-0c8a1e5d9a01", "6f1c1b7e-2f0a-4f43-9d2b-0c8a1e5d9a02"
	serving := a
	tests := []struct {
		name string
		req  UpdateDeploymentTrafficRequest
		ok   bool
	}{
		{"absent", UpdateDeploymentTrafficRequest{TrafficPercent: 100}, true},
		{"split", UpdateDeploymentTrafficRequest{TrafficPercent: 100, ExpectedTraffic: map[string]int{a: 75, b: 25}}, true},
		{"empty", UpdateDeploymentTrafficRequest{ExpectedTraffic: map[string]int{}}, false},
		{"not a uuid", UpdateDeploymentTrafficRequest{ExpectedTraffic: map[string]int{"v3": 100}}, false},
		{"zero weight", UpdateDeploymentTrafficRequest{ExpectedTraffic: map[string]int{a: 100, b: 0}}, false},
		{"sum not 100", UpdateDeploymentTrafficRequest{ExpectedTraffic: map[string]int{a: 60, b: 30}}, false},
		{"with serving guard", UpdateDeploymentTrafficRequest{ExpectedServingDeploymentID: &serving, ExpectedTraffic: map[string]int{a: 100}}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.req.ValidateExpectedTraffic(); tt.ok != (err == nil) {
				t.Fatalf("err = %v, want ok=%v", err, tt.ok)
			}
		})
	}
}
