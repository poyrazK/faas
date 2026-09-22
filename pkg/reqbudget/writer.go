package reqbudget

import (
	"bufio"
	"net"
	"net/http"
)

// Unwrap preserves connection controls such as the stream write deadline.
func (b *budgetWriter) Unwrap() http.ResponseWriter { return b.ResponseWriter }

// FlushError lets ResponseController preserve unsupported-operation errors.
// A successful flush implicitly commits HTTP 200, even without a body write.
func (b *budgetWriter) FlushError() error {
	if err := http.NewResponseController(b.ResponseWriter).Flush(); err != nil {
		return err
	}
	b.wrote = true
	return nil
}

// Flush also supports handlers that use the legacy http.Flusher interface.
func (b *budgetWriter) Flush() { _ = b.FlushError() }

// Hijack transfers response ownership; the middleware must never append its
// own timeout response after a successful protocol upgrade.
func (b *budgetWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	conn, rw, err := http.NewResponseController(b.ResponseWriter).Hijack()
	if err == nil {
		b.wrote = true
	}
	return conn, rw, err
}
