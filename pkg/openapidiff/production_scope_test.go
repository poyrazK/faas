package openapidiff

import "testing"

func TestIsProductionScope(t *testing.T) {
	for _, scope := range []string{"", "default", "prod", "production", " PROD ", "Production"} {
		t.Run(scope, func(t *testing.T) {
			if !IsProductionScope(scope) {
				t.Fatalf("production scope %q skipped contract protection", scope)
			}
		})
	}
	for _, scope := range []string{"staging", "preview-42", "test"} {
		t.Run(scope, func(t *testing.T) {
			if IsProductionScope(scope) {
				t.Fatalf("non-production scope %q received production contract gate", scope)
			}
		})
	}
}
