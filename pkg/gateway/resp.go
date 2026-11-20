package gateway

// RESP2/RESP3 framing for the managed Redis session (ADR-20261116192126).
// The session needs command names, a few small arguments and reply
// boundaries; values are streamed, never buffered whole, so a large GET or
// SET costs the gateway no more memory than a small one.

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"strconv"
)

const (
	// respMaxLine bounds one protocol line (a type marker and a length, or
	// an inline command).
	respMaxLine = 64 << 10
	// respMaxArgs bounds a command's argument count.
	respMaxArgs = 1 << 20
	// respMaxNameBytes bounds a command name.
	respMaxNameBytes = 1 << 10
	// respMaxKeptArgBytes bounds an argument the session keeps for replay.
	respMaxKeptArgBytes = 64 << 10
	// respMaxDepth bounds nested aggregate replies.
	respMaxDepth = 64
)

var errRESPProtocol = errors.New("redis protocol error")

// respReadLine reads one CRLF-terminated line, without the terminator.
func respReadLine(r *bufio.Reader) ([]byte, error) {
	line, err := r.ReadSlice('\n')
	if errors.Is(err, bufio.ErrBufferFull) {
		return nil, fmt.Errorf("%w: line longer than %d bytes", errRESPProtocol, respMaxLine)
	}
	if err != nil {
		return nil, err
	}
	line = bytes.TrimSuffix(line, []byte("\n"))
	line = bytes.TrimSuffix(line, []byte("\r"))
	return line, nil
}

func respParseLen(b []byte) (int64, error) {
	n, err := strconv.ParseInt(string(b), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%w: bad length %q", errRESPProtocol, b)
	}
	return n, nil
}

// respCopyValue copies one complete reply value from r to w and returns its
// type marker. Push ('>') and attribute ('|') frames are copied like any
// other value; the caller decides whether they answer a command.
func respCopyValue(r *bufio.Reader, w io.Writer) (byte, error) {
	return respCopyValueDepth(r, w, 0)
}

func respCopyValueDepth(r *bufio.Reader, w io.Writer, depth int) (byte, error) {
	if depth > respMaxDepth {
		return 0, fmt.Errorf("%w: reply nested deeper than %d", errRESPProtocol, respMaxDepth)
	}
	line, err := respReadLine(r)
	if err != nil {
		return 0, err
	}
	if len(line) == 0 {
		return 0, fmt.Errorf("%w: empty reply line", errRESPProtocol)
	}
	if _, err := w.Write(append(append([]byte(nil), line...), '\r', '\n')); err != nil {
		return 0, err
	}
	kind := line[0]
	switch kind {
	case '+', '-', ':', '_', '#', ',', '(':
		return kind, nil
	case '$', '!', '=':
		n, err := respParseLen(line[1:])
		if err != nil || n < 0 {
			return kind, err
		}
		if _, err := io.CopyN(w, r, n+2); err != nil {
			return kind, err
		}
		return kind, nil
	case '*', '~', '>', '%', '|':
		n, err := respParseLen(line[1:])
		if err != nil || n < 0 {
			return kind, err
		}
		if kind == '%' || kind == '|' {
			n *= 2
		}
		for i := int64(0); i < n; i++ {
			if _, err := respCopyValueDepth(r, w, depth+1); err != nil {
				return kind, err
			}
		}
		return kind, nil
	default:
		return kind, fmt.Errorf("%w: unknown reply type %q", errRESPProtocol, kind)
	}
}

// respCommand is a client command whose name (and, for kept commands, every
// argument) has been read. For an array command the remaining arguments are
// still on the reader unless kept is true.
type respCommand struct {
	// name is the upper-cased command name.
	name string
	// raw is everything read so far, exactly as the client sent it.
	raw []byte
	// rest is the number of array arguments still unread.
	rest int
	// args are all arguments, name first, once read (inline commands, and
	// array commands after readRest).
	args [][]byte
}

// respReadCommandHead reads a command's header and name.
func respReadCommandHead(r *bufio.Reader) (respCommand, error) {
	first, err := r.Peek(1)
	if err != nil {
		return respCommand{}, err
	}
	if first[0] != '*' {
		return respReadInline(r)
	}
	line, err := respReadLine(r)
	if err != nil {
		return respCommand{}, err
	}
	n, err := respParseLen(line[1:])
	if err != nil {
		return respCommand{}, err
	}
	raw := append(append([]byte(nil), line...), '\r', '\n')
	if n <= 0 {
		return respCommand{raw: raw}, nil
	}
	if n > respMaxArgs {
		return respCommand{}, fmt.Errorf("%w: %d arguments", errRESPProtocol, n)
	}
	name, nameRaw, err := respReadBulk(r, respMaxNameBytes)
	if err != nil {
		return respCommand{}, err
	}
	return respCommand{
		name: string(bytes.ToUpper(name)),
		raw:  append(raw, nameRaw...),
		rest: int(n) - 1,
		args: [][]byte{name},
	}, nil
}

func respReadInline(r *bufio.Reader) (respCommand, error) {
	line, err := respReadLine(r)
	if err != nil {
		return respCommand{}, err
	}
	fields := bytes.Fields(line)
	raw := append(append([]byte(nil), line...), '\r', '\n')
	if len(fields) == 0 {
		return respCommand{raw: raw}, nil
	}
	return respCommand{name: string(bytes.ToUpper(fields[0])), raw: raw, args: fields}, nil
}

// respReadBulk reads one bulk string argument of at most limit bytes and
// returns its payload and its raw encoding.
func respReadBulk(r *bufio.Reader, limit int64) ([]byte, []byte, error) {
	line, err := respReadLine(r)
	if err != nil {
		return nil, nil, err
	}
	if len(line) == 0 || line[0] != '$' {
		return nil, nil, fmt.Errorf("%w: command argument is not a bulk string", errRESPProtocol)
	}
	n, err := respParseLen(line[1:])
	if err != nil {
		return nil, nil, err
	}
	if n < 0 || n > limit {
		return nil, nil, fmt.Errorf("%w: argument of %d bytes", errRESPProtocol, n)
	}
	payload := make([]byte, n+2)
	if _, err := io.ReadFull(r, payload); err != nil {
		return nil, nil, err
	}
	raw := append(append(append([]byte(nil), line...), '\r', '\n'), payload...)
	return payload[:n], raw, nil
}

// readRest reads and keeps the remaining arguments of a small command.
func (c *respCommand) readRest(r *bufio.Reader) error {
	for ; c.rest > 0; c.rest-- {
		arg, raw, err := respReadBulk(r, respMaxKeptArgBytes)
		if err != nil {
			return err
		}
		c.args = append(c.args, arg)
		c.raw = append(c.raw, raw...)
	}
	return nil
}

// copyRest streams the remaining arguments to w without keeping them.
func (c *respCommand) copyRest(r *bufio.Reader, w io.Writer) error {
	for ; c.rest > 0; c.rest-- {
		line, err := respReadLine(r)
		if err != nil {
			return err
		}
		if len(line) == 0 || line[0] != '$' {
			return fmt.Errorf("%w: command argument is not a bulk string", errRESPProtocol)
		}
		n, err := respParseLen(line[1:])
		if err != nil {
			return err
		}
		if n < 0 {
			return fmt.Errorf("%w: negative argument length", errRESPProtocol)
		}
		if _, err := w.Write(append(append([]byte(nil), line...), '\r', '\n')); err != nil {
			return err
		}
		if _, err := io.CopyN(w, r, n+2); err != nil {
			return err
		}
	}
	return nil
}

// skipRest discards the remaining arguments.
func (c *respCommand) skipRest(r *bufio.Reader) error {
	return c.copyRest(r, io.Discard)
}

// arg returns argument i upper-cased, or "".
func (c respCommand) arg(i int) string {
	if i >= len(c.args) {
		return ""
	}
	return string(bytes.ToUpper(c.args[i]))
}

// respEncodeCommand encodes args as a RESP array of bulk strings.
func respEncodeCommand(args ...[]byte) []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, "*%d\r\n", len(args))
	for _, a := range args {
		fmt.Fprintf(&b, "$%d\r\n", len(a))
		b.Write(a)
		b.WriteString("\r\n")
	}
	return b.Bytes()
}
