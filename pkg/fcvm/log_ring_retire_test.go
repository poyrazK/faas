// spec: §12
package fcvm

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/fcvm/logbuf"
)

// Prod hunt #3: Kill closed the instance's log ring and dropped every line
// still inside its byte budget, so the archive (fed only by evictions) had
// nothing for a parked instance and `gregale logs --archive` reported an
// archive gap for ordinary apps.
func TestRetireRingHandsRetainedLinesToTheArchive(t *testing.T) {
	v := NewJailerVMM(t.TempDir(), time.Second)
	var gotInstance string
	var got []logbuf.Line
	v.WithLogRetireCallback(func(instance string, lines []logbuf.Line) {
		gotInstance, got = instance, lines
	})
	ring := v.registerRing("app-1")
	if _, err := ring.Write("stdout", []byte("listening on 8080\nGET / 200\n")); err != nil {
		t.Fatal(err)
	}
	v.retireRing("app-1", true)
	if gotInstance != "app-1" || len(got) != 2 || got[0].Line != "listening on 8080" || got[1].Line != "GET / 200" {
		t.Fatalf("retired = %q %+v, want both retained lines for app-1", gotInstance, got)
	}
	if v.LogRing("app-1") != nil {
		t.Fatal("retired ring is still registered")
	}
	if _, err := ring.Write("stdout", []byte("late\n")); err == nil {
		t.Fatal("retired ring still accepts writes")
	}

	got = nil
	builder := v.registerRing("build-1")
	if _, err := builder.Write("stdout", []byte("step 1\n")); err != nil {
		t.Fatal(err)
	}
	v.retireRing("build-1", false)
	if got != nil {
		t.Fatalf("builder output was archived as app logs: %+v", got)
	}
}
