package main

import "testing"

func TestValidateProjectEnvironmentFlag(t *testing.T) {
	if err := validateProjectEnvironmentFlag("staging"); err != nil {
		t.Fatalf("staging rejected: %v", err)
	}
	if err := validateProjectEnvironmentFlag(""); err != nil {
		t.Fatalf("empty environment rejected: %v", err)
	}
	if err := validateProjectEnvironmentFlag("qa"); err == nil {
		t.Fatal("short environment accepted; want deployment-scope validation")
	}
	if err := validateProjectEnvironmentFlag("Bad_Env"); err == nil {
		t.Fatal("invalid environment accepted")
	}
}
