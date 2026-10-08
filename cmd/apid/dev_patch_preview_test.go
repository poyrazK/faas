package main

import (
	"archive/tar"
	"reflect"
	"testing"

	"github.com/onebox-faas/faas/pkg/devpatch"
	"github.com/onebox-faas/faas/pkg/sourcedelta"
)

func TestDevPatchChangesAreSortedAndTyped(t *testing.T) {
	manifest := sourcedelta.Manifest{Entries: map[string]sourcedelta.Entry{
		"src/b.js": {Type: tar.TypeReg, Size: 20},
		"src":      {Type: tar.TypeDir},
		"a.js":     {Type: tar.TypeReg, Size: 5},
	}}
	got := devPatchChanges(manifest, []string{"old.js"})
	want := []devpatch.Change{
		{Path: "a.js", Regular: true, Size: 5},
		{Path: "old.js", Deleted: true},
		{Path: "src", Dir: true},
		{Path: "src/b.js", Regular: true, Size: 20},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("devPatchChanges = %+v, want %+v", got, want)
	}
}
