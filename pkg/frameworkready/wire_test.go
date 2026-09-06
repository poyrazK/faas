package frameworkready

import (
	"bytes"
	"testing"
)

func TestStatusRoundTrip(t *testing.T) {
	for _, want := range []Status{{}, {Ready: true}, {Ready: true, WarmupMs: 4294967295}} {
		var b bytes.Buffer
		if err := Write(&b, want); err != nil {
			t.Fatal(err)
		}
		got, err := Read(&b)
		if err != nil || got != want {
			t.Fatalf("got=%+v err=%v want=%+v", got, err, want)
		}
	}
}
func TestRejectMalformed(t *testing.T) {
	for _, b := range [][]byte{nil, []byte("FRD1"), []byte("WRNG\x01\x00\x00\x00\x00"), []byte("FRD1\x02\x00\x00\x00\x00"), []byte("FRD1\x00\x00\x00\x00\x01")} {
		if _, err := Read(bytes.NewReader(b)); err == nil {
			t.Fatalf("accepted %x", b)
		}
	}
}
