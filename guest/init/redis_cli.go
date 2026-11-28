package main

// `gregale redis cli` (ADR-20261116192126): guest-init sends one command to a
// managed cache from inside the app's network and prints the reply the way
// redis-cli does on a terminal. This file holds the portable parts: the RESP
// request encoding, a bounded reply reader and the reply formatting.

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

const (
	redisCLIMaxDepth    = 32
	redisCLIMaxElements = 1 << 20
)

var errRedisCLIReplyTooLarge = errors.New("the reply is larger than the 1 MiB the command can print")

// redisCLIRequest encodes args as one RESP array of bulk strings.
func redisCLIRequest(args []string) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "*%d\r\n", len(args))
	for _, arg := range args {
		fmt.Fprintf(&b, "$%d\r\n%s\r\n", len(arg), arg)
	}
	return []byte(b.String())
}

// redisCLIReply is one parsed reply.
type redisCLIReply struct {
	kind     byte // '+', '-', ':', '$', '*', and the RESP3 types
	text     string
	nil      bool
	elements []redisCLIReply
}

// limitedReader fails once more than limit bytes are read.
type limitedReader struct {
	r     *bufio.Reader
	limit int
}

func (l *limitedReader) take(n int) error {
	l.limit -= n
	if l.limit < 0 {
		return errRedisCLIReplyTooLarge
	}
	return nil
}

func (l *limitedReader) line() (string, error) {
	s, err := l.r.ReadString('\n')
	if err != nil {
		return "", err
	}
	if err := l.take(len(s)); err != nil {
		return "", err
	}
	return strings.TrimSuffix(strings.TrimSuffix(s, "\n"), "\r"), nil
}

// readRedisCLIReply reads one reply of at most maxBytes.
func readRedisCLIReply(r io.Reader, maxBytes int) (redisCLIReply, error) {
	return (&limitedReader{r: bufio.NewReader(r), limit: maxBytes}).reply(0)
}

func (l *limitedReader) reply(depth int) (redisCLIReply, error) {
	if depth > redisCLIMaxDepth {
		return redisCLIReply{}, errors.New("the reply is nested too deeply")
	}
	header, err := l.line()
	if err != nil {
		return redisCLIReply{}, err
	}
	if header == "" {
		return redisCLIReply{}, errors.New("malformed reply")
	}
	kind, rest := header[0], header[1:]
	switch kind {
	case '+', '-', ':', ',', '(', '#':
		return redisCLIReply{kind: kind, text: rest}, nil
	case '_':
		return redisCLIReply{kind: kind, nil: true}, nil
	case '$', '=', '!':
		n, err := strconv.Atoi(rest)
		if err != nil || n < -1 {
			return redisCLIReply{}, errors.New("malformed bulk reply length")
		}
		if n == -1 {
			return redisCLIReply{kind: kind, nil: true}, nil
		}
		if err := l.take(n + 2); err != nil {
			return redisCLIReply{}, err
		}
		buf := make([]byte, n+2)
		if _, err := io.ReadFull(l.r, buf); err != nil {
			return redisCLIReply{}, err
		}
		return redisCLIReply{kind: kind, text: string(buf[:n])}, nil
	case '*', '~', '>', '%', '|':
		n, err := strconv.Atoi(rest)
		if err != nil || n < -1 || n > redisCLIMaxElements {
			return redisCLIReply{}, errors.New("malformed aggregate reply length")
		}
		if n == -1 {
			return redisCLIReply{kind: kind, nil: true}, nil
		}
		if kind == '%' || kind == '|' {
			n *= 2
		}
		out := redisCLIReply{kind: kind, elements: make([]redisCLIReply, 0, min(n, 1024))}
		for range n {
			element, err := l.reply(depth + 1)
			if err != nil {
				return redisCLIReply{}, err
			}
			out.elements = append(out.elements, element)
		}
		if kind == '|' {
			// Attributes come before the reply they describe.
			return l.reply(depth)
		}
		return out, nil
	}
	return redisCLIReply{}, fmt.Errorf("unknown reply type %q", kind)
}

// formatRedisCLIReply prints a reply as redis-cli does on a terminal.
func formatRedisCLIReply(reply redisCLIReply) string {
	var b strings.Builder
	writeRedisCLIReply(&b, reply, "")
	return b.String()
}

func writeRedisCLIReply(b *strings.Builder, reply redisCLIReply, indent string) {
	switch {
	case reply.nil:
		b.WriteString("(nil)\n")
		return
	case reply.kind == '+':
		b.WriteString(reply.text + "\n")
	case reply.kind == '-' || reply.kind == '!':
		b.WriteString("(error) " + reply.text + "\n")
	case reply.kind == ':' || reply.kind == '(':
		b.WriteString("(integer) " + reply.text + "\n")
	case reply.kind == ',':
		b.WriteString("(double) " + reply.text + "\n")
	case reply.kind == '#':
		if reply.text == "t" {
			b.WriteString("(true)\n")
		} else {
			b.WriteString("(false)\n")
		}
	case reply.kind == '=':
		// Verbatim strings carry a three-letter format before a colon.
		text := reply.text
		if len(text) >= 4 && text[3] == ':' {
			text = text[4:]
		}
		b.WriteString(text + "\n")
	case reply.kind == '$':
		b.WriteString(strconv.Quote(reply.text) + "\n")
	default:
		if len(reply.elements) == 0 {
			b.WriteString("(empty array)\n")
			return
		}
		width := len(strconv.Itoa(len(reply.elements)))
		for i, element := range reply.elements {
			label := fmt.Sprintf("%*d) ", width, i+1)
			if i > 0 {
				b.WriteString(indent)
			}
			b.WriteString(label)
			writeRedisCLIReply(b, element, indent+strings.Repeat(" ", len(label)))
		}
	}
}

// redisCLIConnectionHint explains a cache connection that failed before any
// reply: the proxy closes connections it does not authorize.
func redisCLIConnectionHint(target string, err error) string {
	cache, _, _ := strings.Cut(target, ".")
	return fmt.Sprintf("could not get a reply from %s: %v\n"+
		"The cache refused or dropped the connection. Check that this app is bound to it "+
		"(gregale redis bind %s --app APP) and that the cache exists (gregale redis get %s).\n",
		target, err, cache, cache)
}
