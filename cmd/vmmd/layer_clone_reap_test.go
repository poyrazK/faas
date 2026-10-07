package main

import (
	"context"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/fcvm"
)

type ownershipFake map[string]bool

func (o ownershipFake) HasInstanceOwnership(id string) bool { return o[id] }

// ADR-631: a clone is reclaimable only when nothing owns its instance.
func TestLayerCloneOwnershipGate(t *testing.T) {
	durable := func(_ context.Context, id string) (bool, error) {
		switch id {
		case "durable-live":
			return true, nil
		case "durable-unknown":
			return false, errors.New("store unavailable")
		}
		return false, nil
	}
	gate := layerCloneOwnershipGate(durable, ownershipFake{"manager-owned": true}, nil)
	for id, want := range map[string]bool{"manager-owned": true, "durable-live": true, "unowned": false} {
		if live, err := gate(context.Background(), id); err != nil || live != want {
			t.Errorf("gate(%s) = (%v, %v), want (%v, nil)", id, live, err, want)
		}
	}
	if _, err := gate(context.Background(), "durable-unknown"); err == nil {
		t.Error("an unknown durable state must not authorise removal")
	}
	var nilJournal *fcvm.ResourceJournal
	if owned, err := nilJournal.Owns("x"); owned || err != nil {
		t.Errorf("nil journal Owns = (%v, %v), want (false, nil)", owned, err)
	}
}

func TestLayerCloneFlatDirs(t *testing.T) {
	if got := layerCloneFlatDirs("/srv/fc/base/vmlinux-6.1"); len(got) != 1 || got[0] != "/srv/fc/base" {
		t.Fatalf("flat dirs = %q, want [/srv/fc/base]", got)
	}
	for _, kernel := range []string{"", "vmlinux", "/vmlinux"} {
		if got := layerCloneFlatDirs(kernel); len(got) != 0 {
			t.Errorf("layerCloneFlatDirs(%q) = %q, want none", kernel, got)
		}
	}
}
