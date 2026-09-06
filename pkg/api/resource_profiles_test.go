package api

import "testing"

func TestResourceProfileSpecFor(t *testing.T) {
	tests := []struct {
		name string
		mem  int
		cpu  int
	}{
		{"micro", 128, 250},
		{"small", 256, 500},
		{"medium", 512, 1000},
		{"large", 768, 1000},
		{"xlarge", 1024, 1000},
	}
	for _, tt := range tests {
		got, ok := ResourceProfileSpecFor(tt.name)
		if !ok {
			t.Fatalf("ResourceProfileSpecFor(%q) not found", tt.name)
		}
		if got.MemoryMB != tt.mem || got.CPUMillicores != tt.cpu {
			t.Errorf("ResourceProfileSpecFor(%q) = %+v, want memory=%d cpu=%d", tt.name, got, tt.mem, tt.cpu)
		}
	}
	if _, ok := ResourceProfileSpecFor("custom"); ok {
		t.Fatal("unknown profile custom unexpectedly resolved")
	}
}

func TestResourceProfileForResources(t *testing.T) {
	if got := ResourceProfileForResources(256, 500); got != ResourceProfileSmall {
		t.Fatalf("profile for small shape = %q, want %q", got, ResourceProfileSmall)
	}
	if got := ResourceProfileForResources(384, 500); got != "" {
		t.Fatalf("custom shape returned profile %q", got)
	}
}
