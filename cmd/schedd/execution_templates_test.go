package main

import "testing"

func TestExecutionTemplateShapesFailClosed(t *testing.T) {
	for _, raw := range []string{"", `[]`, `[{"runtime":"node24","memory_mb":256,"ephemeral_disk_mb":64,"source":"private"}]`, `[{"runtime":"go","memory_mb":256,"ephemeral_disk_mb":64}]`, `[{"runtime":"node24","memory_mb":129,"ephemeral_disk_mb":64}]`, `[{"runtime":"node24","memory_mb":256,"ephemeral_disk_mb":64}] {}`} {
		if _, err := executionTemplateShapes(raw); err == nil {
			t.Fatalf("accepted invalid template shapes: %s", raw)
		}
	}
	if shapes, err := executionTemplateShapes(`[{"runtime":"node24","memory_mb":256,"ephemeral_disk_mb":64}]`); err != nil || len(shapes) != 1 {
		t.Fatalf("valid shape: %v", err)
	}
}
