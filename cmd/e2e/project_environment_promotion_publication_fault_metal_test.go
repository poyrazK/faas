//go:build promotionacceptance && metal

package e2e_test

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/internal/promotionfault"
	"github.com/onebox-faas/faas/pkg/api"
	"golang.org/x/sys/unix"
)

type promotionPublicationHit struct {
	promotionfault.Event
	peerPID int
	conn    net.Conn
}

type promotionPublicationFault struct {
	mu               sync.Mutex
	point, promotion string
	hits             chan promotionPublicationHit
	errors           chan error
	connections      map[net.Conn]bool
	closed           bool
}

func newPromotionPublicationFault(t *testing.T, p *environmentPromotionNative) *promotionPublicationFault {
	t.Helper()
	dir, err := os.MkdirTemp("", "faas-publication-")
	mustProjectWorkerNative(t, err)
	path := filepath.Join(dir, "barrier.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	mustProjectWorkerNative(t, err)
	mustProjectWorkerNative(t, os.Chmod(path, 0600))
	f := &promotionPublicationFault{hits: make(chan promotionPublicationHit, 1), errors: make(chan error, 1), connections: map[net.Conn]bool{}}
	var handlers sync.WaitGroup
	handlers.Go(func() {
		for {
			conn, err := listener.AcceptUnix()
			if err != nil {
				return
			}
			f.mu.Lock()
			if f.closed {
				f.mu.Unlock()
				_ = conn.Close()
				return
			}
			f.connections[conn] = true
			f.mu.Unlock()
			handlers.Go(func() {
				if err := f.receive(conn); err != nil {
					select {
					case f.errors <- err:
					default:
					}
				}
			})
		}
	})
	t.Cleanup(func() {
		_ = p.h.SetAPIDEnv(promotionfault.SocketEnv, "")
		_ = listener.Close()
		f.mu.Lock()
		f.closed = true
		for conn := range f.connections {
			_ = conn.Close()
		}
		f.mu.Unlock()
		handlers.Wait()
		_ = os.RemoveAll(dir)
	})
	mustProjectWorkerNative(t, p.h.SetAPIDEnv(promotionfault.SocketEnv, path))
	mustProjectWorkerNative(t, p.h.RestartAPID())
	return f
}

func (f *promotionPublicationFault) receive(conn *net.UnixConn) error {
	keep := false
	defer func() {
		if !keep {
			_ = conn.Close()
			f.mu.Lock()
			delete(f.connections, conn)
			f.mu.Unlock()
		}
	}()
	if err := conn.SetDeadline(time.Now().Add(api.ProjectPromotionHealthJobTimeout)); err != nil {
		return err
	}
	raw, err := conn.SyscallConn()
	if err != nil {
		return err
	}
	var credentials *unix.Ucred
	var credentialErr error
	if err = raw.Control(func(fd uintptr) {
		credentials, credentialErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	}); err != nil {
		return err
	}
	if credentialErr != nil {
		return credentialErr
	}
	var event promotionfault.Event
	decoder := json.NewDecoder(io.LimitReader(conn, api.PromotionPublicationEventMaxBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&event); err != nil {
		return err
	}
	if credentials == nil || event.SchemaVersion != 1 || event.PID != int(credentials.Pid) || credentials.Uid != 0 {
		return errors.New("publication barrier peer identity invalid")
	}
	f.mu.Lock()
	matched := event.Point == f.point && event.PromotionID == f.promotion
	if matched {
		f.point = ""
	}
	f.mu.Unlock()
	if !matched {
		return json.NewEncoder(conn).Encode(map[string]bool{"continue": true})
	}
	select {
	case f.hits <- promotionPublicationHit{Event: event, peerPID: int(credentials.Pid), conn: conn}:
		keep = true
		return nil
	default:
		return errors.New("duplicate publication barrier event")
	}
}

func (f *promotionPublicationFault) arm(promotion, point string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.promotion, f.point = promotion, point
}

func (f *promotionPublicationFault) await(t *testing.T, p *environmentPromotionNative, point string) promotionPublicationHit {
	t.Helper()
	select {
	case err := <-f.errors:
		t.Fatal("publication barrier failed", err)
	case hit := <-f.hits:
		pid, err := p.h.APIDPID()
		mustProjectWorkerNative(t, err)
		if hit.peerPID != pid || hit.Point != point || hit.ProjectID != p.project.ID || hit.ObservedAt.IsZero() || hit.ObservedAt.After(time.Now().UTC()) || !time.Now().Before(hit.Deadline) {
			t.Fatal("publication event was not emitted by the owned APID at the requested boundary")
		}
		return hit
	case <-time.After(5 * time.Minute):
		t.Fatal("native publication boundary was not reached", point)
	case <-t.Context().Done():
		t.Fatal(t.Context().Err())
	}
	return promotionPublicationHit{}
}
