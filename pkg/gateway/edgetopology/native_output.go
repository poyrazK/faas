package edgetopology

import "bytes"

// Do not embed bytes.Buffer: its promoted io.ReaderFrom would let io.Copy in
// os/exec bypass Write and the output bound. Expose only the bounded writer.
type nativeBoundedOutput struct {
	buffer bytes.Buffer
	limit  int
}

func (b *nativeBoundedOutput) Write(raw []byte) (int, error) {
	if len(raw) > b.limit-b.buffer.Len() {
		return 0, nativeReadError("systemd output byte bound")
	}
	return b.buffer.Write(raw)
}

func (b *nativeBoundedOutput) Bytes() []byte { return b.buffer.Bytes() }
