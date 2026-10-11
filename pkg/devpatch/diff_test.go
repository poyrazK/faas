package devpatch

import (
	"reflect"
	"testing"
)

func TestDiffIsCumulativeAndSorted(t *testing.T) {
	base := map[string]Entry{
		"src":        {Type: '5'},
		"src/a.js":   {Type: '0', Mode: 0o644, Size: 1, Digest: "a1"},
		"src/b.js":   {Type: '0', Mode: 0o644, Size: 1, Digest: "b1"},
		"src/old.js": {Type: '0', Mode: 0o644, Size: 1, Digest: "o1"},
		"gone":       {Type: '5'},
	}
	current := map[string]Entry{
		"src":        {Type: '5'},
		"src/a.js":   {Type: '0', Mode: 0o644, Size: 2, Digest: "a2"}, // edited
		"src/b.js":   {Type: '0', Mode: 0o755, Size: 1, Digest: "b1"}, // mode change
		"src/new.js": {Type: '0', Mode: 0o644, Size: 3, Digest: "n1"},
	}
	want := []Change{
		{Path: "src/a.js", Regular: true, Size: 2},
		{Path: "src/b.js", Regular: true, Size: 1},
		{Path: "src/new.js", Regular: true, Size: 3},
		{Path: "src/old.js", Deleted: true},
	}
	if got := Diff(base, current); !reflect.DeepEqual(got, want) {
		t.Fatalf("Diff = %+v, want %+v", got, want)
	}
	if got := Diff(base, base); len(got) != 0 {
		t.Fatalf("Diff of identical manifests = %+v, want none", got)
	}
}
