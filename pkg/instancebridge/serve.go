package instancebridge

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/udpwire"
)

const (
	// dialTimeout and headTimeout are the per-connection bridges' limits:
	// past them the gateway has given up, or the guest is wedged.
	dialTimeout = 30 * time.Second
	headTimeout = 30 * time.Second
	// helloTimeout bounds how long a new connection may take to say what
	// it is: a slow loris must not hold a goroutine and a descriptor.
	helloTimeout = 10 * time.Second
)

// Dialer dials the guest from inside the instance's network namespace.
type Dialer func(ctx context.Context, network, address string) (net.Conn, error)

// Sessions serves session connections for one guest address.
type Sessions struct {
	GuestIP string
	Dial    Dialer
}

// Serve handles one session whose hello is next on r, then closes conn.
func (s *Sessions) Serve(ctx context.Context, conn net.Conn, r *bufio.Reader) {
	defer func() { _ = conn.Close() }()
	_ = conn.SetReadDeadline(time.Now().Add(helloTimeout))
	kind, port, err := ReadHello(r)
	if err != nil {
		return
	}
	_ = conn.SetReadDeadline(time.Time{})
	network := "tcp"
	if kind == KindUDP {
		network = "udp4"
	}
	dialCtx, cancel := context.WithTimeout(ctx, dialTimeout)
	guest, err := s.Dial(dialCtx, network, net.JoinHostPort(s.GuestIP, strconv.Itoa(int(port))))
	cancel()
	if err != nil {
		_ = WriteReady(conn, err)
		return
	}
	defer func() { _ = guest.Close() }()
	if WriteReady(conn, nil) != nil {
		return
	}
	out := NewFrameWriter(conn)
	in := &sessionInput{conn: conn, r: r}
	switch kind {
	case KindRaw:
		err = serveRaw(guest, in, out)
	case KindTCP:
		err = serveTCP(guest, in, out)
	case KindUDP:
		udp, ok := guest.(*net.UDPConn)
		if !ok {
			err = errors.New("UDP dial returned a non-UDP socket")
			break
		}
		err = udpwire.Bridge(ctx, udp, in, &udpOutput{FrameWriter: out, conn: conn})
		_ = conn.SetWriteDeadline(time.Time{})
	}
	if err != nil {
		_ = out.End(CodeIO, err.Error())
		return
	}
	_ = out.End(CodeOK, "")
}

// sessionInput is the client's request bytes after the hello.
type sessionInput struct {
	conn net.Conn
	r    *bufio.Reader
}

func (i *sessionInput) Read(p []byte) (int, error) { return i.r.Read(p) }

// Close stops reading: the client's write side is done or unwanted.
func (i *sessionInput) Close() error {
	if c, ok := i.conn.(interface{ CloseRead() error }); ok {
		return c.CloseRead()
	}
	return nil
}

// udpOutput is udpwire.Bridge's output. Close must unblock a pending
// write (udpwire.Bridge's contract) without closing the connection the
// end frame still goes out on.
type udpOutput struct {
	*FrameWriter
	conn net.Conn
}

func (o *udpOutput) Close() error { return o.conn.SetWriteDeadline(time.Now()) }

// serveRaw is vmmd-raw-bridge on a session: the request goes to the guest
// as it arrives; the response head goes back CR-stripped and ending in a
// blank line, then the body as raw bytes.
func serveRaw(guest net.Conn, in io.Reader, out io.Writer) error {
	writeErr := make(chan error, 1)
	go func() {
		_, err := io.Copy(guest, in)
		writeErr <- err
	}()
	if err := guest.SetReadDeadline(time.Now().Add(headTimeout)); err != nil {
		return err
	}
	reader := bufio.NewReader(guest)
	head, body, err := ReadResponseHead(reader)
	if err != nil {
		return fmt.Errorf("read head: %w", err)
	}
	if _, err := out.Write(append(head, '\n', '\n')); err != nil {
		return err
	}
	if len(body) > 0 {
		if _, err := out.Write(body); err != nil {
			return err
		}
	}
	if err := guest.SetReadDeadline(time.Time{}); err != nil {
		return err
	}
	if _, err := io.Copy(out, reader); err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("copy body: %w", err)
	}
	// The guest closed the connection. A request body it never took is a
	// truncated request, not a clean end.
	if err := <-writeErr; err != nil && !CleanClose(err) {
		return fmt.Errorf("write body: %w", err)
	}
	return nil
}

// serveTCP is vmmd-tcp-bridge on a session: bytes both ways, the client's
// end of input half-closes the guest socket, and the guest's close ends
// the session.
func serveTCP(guest net.Conn, in io.Reader, out io.Writer) error {
	readDone := make(chan error, 1)
	writeDone := make(chan error, 1)
	go func() {
		_, err := io.Copy(out, guest)
		readDone <- err
	}()
	go func() {
		_, err := io.Copy(guest, in)
		if err == nil {
			if tcp, ok := guest.(*net.TCPConn); ok {
				err = tcp.CloseWrite()
			}
		}
		writeDone <- err
	}()
	var readErr, writeErr error
	gotRead, gotWrite := false, false
	for !gotRead || !gotWrite {
		select {
		case readErr = <-readDone:
			gotRead = true
		case writeErr = <-writeDone:
			gotWrite = true
		}
		if gotRead && !gotWrite || gotWrite && !gotRead && writeErr != nil {
			_ = guest.Close()
		}
	}
	if !CleanClose(readErr) {
		return readErr
	}
	if !CleanClose(writeErr) {
		return writeErr
	}
	return nil
}

// ReadResponseHead reads up to the guest's "\n\n" head terminator, with
// each line's CR stripped, returning the head without the terminator and
// any body bytes read with it (vmmd-raw-bridge's contract).
func ReadResponseHead(r *bufio.Reader) (head, body []byte, err error) {
	var acc []byte
	for {
		frag, ferr := r.ReadBytes('\n')
		if len(frag) > 0 {
			if frag[len(frag)-1] == '\n' {
				if len(frag) >= 2 && frag[len(frag)-2] == '\r' {
					frag = append(frag[:len(frag)-2:len(frag)-2], '\n')
				}
				acc = append(acc, frag...)
				if idx := bytes.Index(acc, []byte("\n\n")); idx >= 0 {
					return acc[:idx], acc[idx+2:], nil
				}
			} else {
				acc = append(acc, frag...)
			}
		}
		if errors.Is(ferr, io.EOF) {
			return acc, nil, fmt.Errorf("unexpected EOF before head terminator (got %d bytes)", len(acc))
		}
		if ferr != nil {
			return acc, nil, ferr
		}
		if len(acc) > 64*1024 {
			return acc, nil, errors.New("response head exceeds 64 KiB")
		}
	}
}

// CleanClose reports the errors a bridge copy ends with when either side
// simply closed.
func CleanClose(err error) bool {
	if err == nil || errors.Is(err, io.EOF) || errors.Is(err, io.ErrClosedPipe) || errors.Is(err, net.ErrClosed) {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "use of closed network connection") || strings.Contains(msg, "broken pipe")
}
