package main_test

import (
	"github.com/onebox-faas/faas/cmd/gregale"
	"testing"
)

func TestInternalCIServiceIsAbsentFromCustomerCLI(t *testing.T) {
	if _, ok := main.LookupCliCommand("ci"); ok {
		t.Fatal("internal CI service must remain in gregalectl until a customer rollout is authorized")
	}
}
