package main

import "testing"

func TestInternalCIServiceIsAbsentFromCustomerCLI(t *testing.T) {
	if _, ok := lookupCliCommand("ci"); ok {
		t.Fatal("internal CI service must remain in gregalectl until a customer rollout is authorized")
	}
}
