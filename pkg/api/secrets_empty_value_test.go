package api

import "testing"

// An empty secret value is a validation error (400), not the seal path's
// 503 "Secret storage temporarily unavailable" (Hunt 11 H11-39).
func TestSecretRequestValidateRefusesEmptyValue(t *testing.T) {
	tests := []struct {
		name string
		got  *Problem
		want int
	}{
		{"put empty", PutAppSecretRequest{}.Validate(1024), 400},
		{"put value", PutAppSecretRequest{Value: "x"}.Validate(1024), 0},
		{"put over cap", PutAppSecretRequest{Value: "xx"}.Validate(1), 413},
		{"rotate empty", RotateAppSecretRequest{}.Validate(1024), 400},
		{"rotate value", RotateAppSecretRequest{Value: "x"}.Validate(1024), 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status := 0
			if tt.got != nil {
				status = tt.got.Status
			}
			if status != tt.want {
				t.Fatalf("status = %d, want %d (problem %+v)", status, tt.want, tt.got)
			}
		})
	}
}
