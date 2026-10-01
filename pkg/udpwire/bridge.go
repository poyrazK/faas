package udpwire

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"syscall"

	"github.com/onebox-faas/faas/pkg/api"
)

// Bridge owns conn, input and output until it returns. Both pipe endpoints
// must unblock pending I/O when closed. There is no UDP half-close: pipe EOF,
// transport failure or cancellation ends the peer session in both directions.
func Bridge(ctx context.Context, conn *net.UDPConn, input io.ReadCloser, output io.WriteCloser) error {
	if ctx == nil || conn == nil || input == nil || output == nil {
		return errors.New("UDP bridge requires context, socket and pipe endpoints")
	}
	if err := ctx.Err(); err != nil {
		_ = conn.Close()
		_ = input.Close()
		_ = output.Close()
		return err
	}
	done := make(chan error, 2)
	go func() {
		for {
			payload, err := Read(input)
			if err != nil {
				done <- err
				return
			}
			n, _, err := conn.WriteMsgUDP(payload, nil, nil)
			if err != nil {
				done <- fmt.Errorf("write guest datagram: %w", err)
				return
			}
			if n != len(payload) {
				done <- io.ErrShortWrite
				return
			}
		}
	}()
	go func() {
		payload := make([]byte, api.UDPDatagramMaxBytes)
		for {
			n, _, flags, _, err := conn.ReadMsgUDP(payload, nil)
			if err != nil {
				done <- fmt.Errorf("read guest datagram: %w", err)
				return
			}
			if flags&syscall.MSG_TRUNC != 0 {
				done <- ErrDatagramTooLarge
				return
			}
			if err := Write(output, payload[:n]); err != nil {
				done <- err
				return
			}
		}
	}()
	var result error
	completed := 0
	select {
	case result = <-done:
		completed++
	case <-ctx.Done():
		result = ctx.Err()
	}
	_ = conn.Close()
	_ = input.Close()
	_ = output.Close()
	for completed < 2 {
		<-done
		completed++
	}
	if errors.Is(result, io.EOF) {
		return nil
	}
	return result
}
