// adr: 570
package gateway

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/http/httpguts"
)

var errPublicUpgradeHandled = errors.New("public upgrade transport handled")

// Own both transports and join both copiers before releasing the registration.
// Closing only the backend cannot interrupt a blocked client socket write.
func copyPublicUpgrade(ctx context.Context, w http.ResponseWriter, r *http.Request, resp *http.Response) error {
	requested, selected := r.Header.Get("Upgrade"), resp.Header.Get("Upgrade")
	if !httpguts.HeaderValuesContainsToken(resp.Header.Values("Connection"), "Upgrade") ||
		selected == "" || !httpguts.ValidHeaderFieldValue(selected) || !strings.EqualFold(requested, selected) {
		return errors.New("compute selected an invalid upgrade protocol")
	}
	backend, ok := resp.Body.(io.ReadWriteCloser)
	if !ok {
		return errors.New("compute upgrade body is not bidirectional")
	}
	conn, buffer, err := http.NewResponseController(w).Hijack()
	if err != nil {
		return err
	}
	var once sync.Once
	closeBoth := func() { once.Do(func() { _ = conn.Close(); _ = backend.Close() }) }
	defer closeBoth()
	// Successful upgrades use the independent session ceiling, not the old
	// server/handshake deadline retained on a hijacked connection.
	if err := conn.SetDeadline(time.Time{}); err != nil {
		return errPublicUpgradeHandled
	}
	done := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		defer close(done)
		closeBoth()
	})
	defer func() {
		if !stop() {
			<-done
		}
	}()
	for name, values := range resp.Header {
		for _, value := range values {
			w.Header().Add(name, value)
		}
	}
	head := *resp
	head.Header, head.Body = w.Header(), nil
	if err := head.Write(buffer); err != nil {
		return errPublicUpgradeHandled
	}
	if err := buffer.Flush(); err != nil {
		return errPublicUpgradeHandled
	}
	results := make(chan error, 2)
	go copyUpgradeDirection(backend, buffer, results)
	go copyUpgradeDirection(conn, backend, results)
	if err := <-results; err != nil {
		closeBoth()
	}
	<-results
	return errPublicUpgradeHandled
}

func copyUpgradeDirection(dst io.Writer, src io.Reader, results chan<- error) {
	_, err := io.Copy(dst, src)
	if err == nil {
		if half, ok := dst.(interface{ CloseWrite() error }); ok {
			err = half.CloseWrite()
		} else {
			err = io.EOF
		}
	}
	results <- err
}
