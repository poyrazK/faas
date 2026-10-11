package fcvm

import (
	"context"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type nativeRestoreChannelIOFixture struct {
	net.Conn
	read                                  func([]byte) (int, error)
	writes                                int
	deadline, readDeadline, writeDeadline time.Time
}

func (c *nativeRestoreChannelIOFixture) Read(body []byte) (int, error) { return c.read(body) }
func (c *nativeRestoreChannelIOFixture) Write(body []byte) (int, error) {
	c.writes++
	return len(body), nil
}
func (c *nativeRestoreChannelIOFixture) SetDeadline(deadline time.Time) error {
	c.deadline = deadline
	return nil
}
func (c *nativeRestoreChannelIOFixture) SetReadDeadline(deadline time.Time) error {
	c.readDeadline = deadline
	return nil
}
func (c *nativeRestoreChannelIOFixture) SetWriteDeadline(deadline time.Time) error {
	c.writeDeadline = deadline
	return nil
}

func TestNativeQualificationRestoreChannelIODoesNotDeliverAfterAuthorityLoss(t *testing.T) {
	for _, stage := range []string{"before_read", "during_read", "before_write", "after_write", "original"} {
		t.Run(stage, func(t *testing.T) {
			lost := errors.New("original target revoked")
			valid := stage != "before_read" && stage != "before_write"
			input := &nativeRestoreChannelIOFixture{read: func(body []byte) (int, error) {
				n := copy(body, "private request")
				if stage == "during_read" {
					valid = false
				}
				return n, nil
			}}
			stream := &nativeQualificationRestoreConn{Conn: input, ctx: t.Context(), require: func() error {
				if !valid || stage == "after_write" && input.writes > 0 {
					return lost
				}
				return nil
			}}
			if stage == "before_write" || stage == "after_write" {
				_, err := stream.Write([]byte("private response"))
				if !errors.Is(err, lost) || input.writes != map[string]int{"before_write": 0, "after_write": 1}[stage] {
					t.Fatal("unowned or uncertain response was delivered/retried", err, input.writes)
				}
				return
			}
			body := make([]byte, 32)
			n, err := stream.Read(body)
			if stage == "original" {
				if err != nil || string(body[:n]) != "private request" {
					t.Fatal("original request was lost", err)
				}
			} else if !errors.Is(err, lost) || n != 0 || string(body) != string(make([]byte, len(body))) {
				t.Fatal("revoked input bytes escaped to callback", n, err)
			}
		})
	}
}

func TestNativeQualificationRestoreChannelCannotExtendOriginalDeadline(t *testing.T) {
	original := time.Now().Add(time.Second)
	ctx, cancel := context.WithDeadline(t.Context(), original)
	defer cancel()
	for _, requested := range []time.Time{{}, original.Add(time.Hour), original.Add(-time.Millisecond)} {
		input := &nativeRestoreChannelIOFixture{}
		stream := &nativeQualificationRestoreConn{Conn: input, ctx: ctx}
		if err := errors.Join(stream.SetDeadline(requested), stream.SetReadDeadline(requested), stream.SetWriteDeadline(requested)); err != nil {
			t.Fatal(err)
		}
		want := requested
		if requested.IsZero() || requested.After(original) {
			want = original
		}
		if input.deadline != want || input.readDeadline != want || input.writeDeadline != want {
			t.Fatal("handler extended original channel deadline", input.deadline)
		}
	}
}

func TestNativeQualificationRestoreChannelSerializesAndRetiresDescriptorAuthority(t *testing.T) {
	var active, overlap atomic.Int32
	stream := &nativeQualificationRestoreConn{ctx: t.Context(), require: func() error {
		if active.Add(1) != 1 {
			overlap.Add(1)
		}
		time.Sleep(time.Microsecond)
		active.Add(-1)
		return nil
	}}
	var workers sync.WaitGroup
	for range 16 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for range 16 {
				if err := stream.requireCurrent(); err != nil {
					t.Error(err)
				}
			}
		}()
	}
	workers.Wait()
	closed := false
	if err := stream.closeAuthority(func() error { closed = true; return nil }); err != nil {
		t.Fatal(err)
	}
	if !closed || overlap.Load() != 0 || !errors.Is(stream.requireCurrent(), net.ErrClosed) {
		t.Fatal("shared descriptor checks overlapped or escaped callback lifetime")
	}
}
