package dbarchive

import (
	"bufio"
	"fmt"
	"io"
)

// Client is guest-init's side of the protocol over one vsock stream.
type Client struct {
	rw io.ReadWriter
	br *bufio.Reader
}

// NewClient wraps an open stream.
func NewClient(rw io.ReadWriter) *Client {
	return &Client{rw: rw, br: bufio.NewReader(rw)}
}

// PutWAL archives size bytes from r as name and waits until the host has
// stored them durably.
func (c *Client) PutWAL(name string, r io.Reader, size int64) error {
	if !ValidWALName(name) || size <= 0 || size > MaxWALBytes {
		return fmt.Errorf("dbarchive: refusing to archive %q (%d bytes)", name, size)
	}
	if err := WriteLine(c.rw, Request{Op: OpPutWAL, Name: name, Size: size}); err != nil {
		return err
	}
	if n, err := io.CopyN(c.rw, r, size); err != nil {
		return fmt.Errorf("dbarchive: send %s after %d bytes: %w", name, n, err)
	}
	var resp Response
	if err := ReadLine(c.br, &resp); err != nil {
		return err
	}
	return errorFor(resp.Error)
}

// GetWAL fetches name into w. A missing file is ErrNotFound, which
// restore_command reports as "no such segment" to end recovery's search.
func (c *Client) GetWAL(name string, w io.Writer) error {
	if !ValidWALName(name) {
		return fmt.Errorf("dbarchive: invalid wal name %q", name)
	}
	if err := WriteLine(c.rw, Request{Op: OpGetWAL, Name: name}); err != nil {
		return err
	}
	var resp Response
	if err := ReadLine(c.br, &resp); err != nil {
		return err
	}
	if err := errorFor(resp.Error); err != nil {
		return err
	}
	if resp.Size <= 0 || resp.Size > MaxWALBytes {
		return fmt.Errorf("dbarchive: wal size %d out of range", resp.Size)
	}
	_, err := io.CopyN(w, c.br, resp.Size)
	return err
}
