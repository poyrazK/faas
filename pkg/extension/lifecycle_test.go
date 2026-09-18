package extension

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestEventValidateClosedVocabulary(t *testing.T) {
	base := Event{Version: ProtocolVersion, Sequence: 1, Phase: PhaseInit, At: time.Unix(1, 0).UTC()}
	for _, phase := range []Phase{PhaseInit, PhasePreSnapshot, PhasePostRestore, PhaseInvoke, PhaseShutdown} {
		base.Phase = phase
		if err := base.Validate(); err != nil {
			t.Errorf("phase %q rejected: %v", phase, err)
		}
	}
	base.Phase = "unknown"
	if err := base.Validate(); err == nil {
		t.Fatal("unknown phase accepted")
	}
}

func TestDispatcherDispatchesBoundedAckAndSequencesEvents(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "gregale-ext-")
	if err != nil {
		t.Fatalf("mkdir temp dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "extension.sock")
	listener, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	got := make(chan Event, 2)
	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		for i := 0; i < 2; i++ {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			func() {
				defer func() { _ = conn.Close() }()
				line, readErr := bufio.NewReader(conn).ReadBytes('\n')
				if readErr != nil {
					return
				}
				var event Event
				if json.Unmarshal(bytesTrimSuffixNewline(line), &event) != nil {
					return
				}
				got <- event
				ack, _ := json.Marshal(Ack{Version: ProtocolVersion, Sequence: event.Sequence, Status: AckOK})
				_, _ = conn.Write(append(ack, '\n'))
			}()
		}
	}()

	dispatcher := NewDispatcher(sock)
	dispatcher.Timeout = time.Second
	dispatcher.Now = func() time.Time { return time.Unix(42, 0).UTC() }
	if err := dispatcher.Dispatch(context.Background(), PhaseInit, nil); err != nil {
		t.Fatalf("dispatch init: %v", err)
	}
	if err := dispatcher.Dispatch(context.Background(), PhasePostRestore, map[string]string{"source": "resume"}); err != nil {
		t.Fatalf("dispatch post_restore: %v", err)
	}
	<-serverDone
	close(got)

	var events []Event
	for event := range got {
		events = append(events, event)
	}
	if len(events) != 2 {
		t.Fatalf("received %d events, want 2", len(events))
	}
	if events[0].Sequence != 1 || events[0].Phase != PhaseInit {
		t.Fatalf("first event = %+v, want sequence 1/init", events[0])
	}
	if events[1].Sequence != 2 || events[1].Phase != PhasePostRestore {
		t.Fatalf("second event = %+v, want sequence 2/post_restore", events[1])
	}
}

func TestDispatcherUnavailableIsDistinct(t *testing.T) {
	dispatcher := NewDispatcher(filepath.Join(t.TempDir(), "missing.sock"))
	err := dispatcher.Dispatch(context.Background(), PhaseShutdown, nil)
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("error = %v, want ErrUnavailable", err)
	}
	if !IsUnavailable(err) {
		t.Fatalf("IsUnavailable(%v) = false", err)
	}
}

func TestAckRejectsSequenceMismatch(t *testing.T) {
	event := Event{Version: ProtocolVersion, Sequence: 7, Phase: PhaseInvoke, At: time.Unix(1, 0).UTC()}
	err := (Ack{Version: ProtocolVersion, Sequence: 8, Status: AckOK}).ValidateFor(event)
	if err == nil {
		t.Fatal("mismatched sequence accepted")
	}
}
