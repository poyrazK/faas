package logbuf

import "testing"

func TestRing_CommitCallbackSeesEveryAcceptedLine(t *testing.T) {
	r := New(64)
	var got []Line
	r.SetCommitCallback(func(l Line) { got = append(got, l) })
	if _, err := r.Write("stdout", []byte("{\"level\":\"error\"}\npartial")); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Level != "error" || got[0].Seq != 1 {
		t.Fatalf("after first write got %+v, want one error line (partial line held back)", got)
	}
	if _, err := r.Write("stderr", []byte(" done\n")); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[1].Line != "partial done" || got[1].Stream != "stderr" {
		t.Fatalf("got %+v, want the completed partial line second", got)
	}
	// A line larger than the whole ring is rejected and must not be counted.
	big := make([]byte, 100)
	for i := range big {
		big[i] = 'x'
	}
	if _, err := r.Write("stdout", append(big, '\n')); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("oversized line reached the callback: %d lines", len(got))
	}
	r.SetCommitCallback(nil)
	if _, err := r.Write("stdout", []byte("after\n")); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatal("callback still fired after removal")
	}
}
