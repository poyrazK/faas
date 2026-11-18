package fcvm

import (
	"errors"
	"os"
	"os/exec"
	"testing"
	"time"
)

// A child forked while writeExecutable's file is open would inherit the write
// descriptor and make exec'ing the file fail with ETXTBSY, so a process start
// that races the write must wait until write has returned.
func TestWriteExecutableHoldsProcessStartsUntilWriteReturns(t *testing.T) {
	entered := make(chan struct{})
	started := make(chan time.Time, 1)
	go func() {
		<-entered
		cmd := exec.Command(os.Args[0], "-test.run=^$")
		err := cmd.Start()
		started <- time.Now()
		if err == nil {
			_ = cmd.Wait()
		}
	}()

	var returned time.Time
	sentinel := errors.New("write result")
	err := writeExecutable(func() error {
		close(entered)
		select {
		case <-started:
			t.Error("a process started while the file was open for writing")
		case <-time.After(200 * time.Millisecond):
		}
		returned = time.Now()
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("writeExecutable returned %v, want write's own error", err)
	}

	select {
	case at := <-started:
		if at.Before(returned) {
			t.Fatalf("process started at %v, before write returned at %v", at, returned)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the waiting process never started after the write returned")
	}
}
