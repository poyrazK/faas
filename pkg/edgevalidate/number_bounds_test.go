package edgevalidate_test

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/edgevalidate"
)

func TestExactNumberValidationBounds(t *testing.T) {
	compiled, err := edgevalidate.Compile([]byte(`{"type":"number","minimum":0}`), false)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"1e4097", "1e-4097", strings.Repeat("9", edgevalidate.MaxNumberBytes+1)} {
		fe, err := compiled.Validate([]byte(value))
		if err != nil || fe == nil {
			t.Errorf("unbounded number accepted: bytes=%d fieldError=%+v err=%v", len(value), fe, err)
		}
	}
	for _, value := range []string{"1e4096", "1e-4096", "9007199254740993", "1.0000000000000001"} {
		fe, err := compiled.Validate([]byte(value))
		if err != nil || fe != nil {
			t.Errorf("bounded exact value rejected: %s fieldError=%+v err=%v", value, fe, err)
		}
	}
	if _, err := edgevalidate.Compile([]byte(`{"minimum":1e4097}`), false); err == nil {
		t.Fatal("schema numeric expansion was not bounded")
	}
	// A nested number must be checked before schema validation as well.
	object, err := edgevalidate.Compile([]byte(`{"type":"object"}`), false)
	if err != nil {
		t.Fatal(err)
	}
	if fe, err := object.Validate([]byte(`{"n":1e4097}`)); err != nil || fe == nil {
		t.Fatalf("nested number bypassed guard: %+v %v", fe, err)
	}
}

func TestAggregateNumberWorkIsBoundedBeforeLibraryArithmetic(t *testing.T) {
	// The instance is only about 45 KiB. A true schema makes the negative
	// control harmless even if the guard is accidentally removed.
	compiled, err := edgevalidate.Compile([]byte(`true`), false)
	if err != nil {
		t.Fatal(err)
	}
	count := edgevalidate.MaxNumberWorkBytes/edgevalidate.MaxNumberExponent + 1
	body := []byte("[" + strings.Repeat("1e4096,", count-1) + "1e4096]")
	if fieldError, err := compiled.Validate(body); err != nil || fieldError == nil {
		t.Fatalf("aggregate numeric expansion was not bounded: %+v %v", fieldError, err)
	}
	schema := []byte(`{"const":[` + strings.Repeat("1e4096,", 16) + `1e4096]}`)
	if _, err := edgevalidate.Compile(schema, false); err == nil {
		t.Fatal("schema aggregate numeric expansion was not bounded")
	}
}
