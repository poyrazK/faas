package httpjson

import (
	"errors"
	"strings"
	"testing"
)

func TestDecodeBoundsResponseBody(t *testing.T) {
	var got map[string]string
	if err := Decode(strings.NewReader(`{"ok":"yes"}`), 64, &got); err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got["ok"] != "yes" {
		t.Fatalf("decoded value = %#v", got)
	}

	err := Decode(strings.NewReader(`{"ok":"`+strings.Repeat("x", 64)+`"}`), 64, &got)
	if !errors.Is(err, ErrResponseTooLarge) {
		t.Fatalf("Decode oversized error = %v, want ErrResponseTooLarge", err)
	}
}

func TestDecodeRejectsTrailingJSON(t *testing.T) {
	var got map[string]bool
	if err := Decode(strings.NewReader(`{"ok":true} {"second":true}`), 128, &got); err == nil {
		t.Fatal("Decode accepted trailing JSON")
	}
}
