package simpleapp

import "testing"

func TestResolveUsesStatelessDefaults(t *testing.T) {
	got, err := Resolve(Spec{Slug: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Source != SourceDirectory || got.Port != DefaultPort || got.HealthPath != DefaultHealthPath {
		t.Fatalf("defaults = %+v", got)
	}
	if got.ExecutionMode != "request" || !got.ScaleToZero || got.LocalStorage != LocalStorageEphemeral || got.DurableState != DurableStateExternal {
		t.Fatalf("stateless defaults = %+v", got)
	}
	if got.ResourceProfile != "plan-default" || got.MemoryMB != 0 || got.CPUMillicores != 0 {
		t.Fatalf("resource defaults = %+v", got)
	}
}

func TestResolvePreservesExplicitProfileAndHealth(t *testing.T) {
	got, err := Resolve(Spec{Slug: "hello", Source: SourceImage, Framework: "docker", Profile: "small", Port: 9000, HealthPath: "/ready"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Source != SourceImage || got.Framework != "docker" || got.ResourceProfile != "small" || got.MemoryMB != 256 || got.CPUMillicores != 500 {
		t.Fatalf("explicit plan = %+v", got)
	}
	if got.Port != 9000 || got.HealthPath != "/ready" {
		t.Fatalf("explicit listener contract = %+v", got)
	}
	req := got.CreateRequest()
	if req.Slug != "hello" || req.Type != "app" || req.ExecutionMode != "request" || req.ResourceProfile != "small" || req.HealthPath != "/ready" {
		t.Fatalf("create request = %+v", req)
	}
}

func TestResolveRejectsUnsafeInputs(t *testing.T) {
	cases := []Spec{
		{Slug: "x"},
		{Slug: "hello", Profile: "huge"},
		{Slug: "hello", Port: 70000},
		{Slug: "hello", HealthPath: "ready"},
		{Slug: "hello", Source: "worker"},
	}
	for _, tc := range cases {
		if _, err := Resolve(tc); err == nil {
			t.Errorf("Resolve(%+v) succeeded, want validation error", tc)
		}
	}
}
