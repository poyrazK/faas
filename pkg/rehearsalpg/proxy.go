package rehearsalpg

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"strings"
	"time"
)

// Proxy exposes one operator-selected local cluster. The resolver must freshly
// authorize a live execution identity and its retained run on every invocation.
type Proxy struct {
	Target  string
	Resolve func(context.Context, string) (Resource, error)
}

func ValidProxyTarget(target string) bool {
	host, port, e := net.SplitHostPort(target)
	ip := net.ParseIP(host)
	return e == nil && ip != nil && ip.IsLoopback() && port != ""
}

func (p Proxy) Serve(ctx context.Context, l net.Listener) error {
	if !ValidProxyTarget(p.Target) || p.Resolve == nil {
		return errors.New("invalid rehearsal PostgreSQL proxy configuration")
	}
	stop := context.AfterFunc(ctx, func() { _ = l.Close() })
	defer stop()
	slots := make(chan struct{}, 64)
	for {
		c, e := l.Accept()
		if e != nil {
			if ctx.Err() != nil {
				return nil
			}
			return e
		}
		select {
		case slots <- struct{}{}:
			go func() { defer func() { <-slots }(); p.ServeConn(ctx, c) }()
		default:
			_ = c.Close()
		}
	}
}

func (p Proxy) ServeConn(ctx context.Context, front net.Conn) {
	defer func() { _ = front.Close() }()
	if !ValidProxyTarget(p.Target) || p.Resolve == nil {
		return
	}
	stop := context.AfterFunc(ctx, func() { _ = front.Close() })
	defer stop()
	call, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	r, e := p.Resolve(call, front.RemoteAddr().String())
	if e != nil || r.Status != "ready" || !r.ExpiresAt.After(time.Now()) {
		return
	}
	_ = front.SetDeadline(time.Now().Add(3 * time.Second))
	packet, e := postgresStartup(front)
	if e != nil || !matchingStartup(packet, r) {
		return
	}
	latest, e := p.Resolve(call, front.RemoteAddr().String())
	if e != nil || latest.ID != r.ID || latest.Status != "ready" || latest.DatabaseName != r.DatabaseName || latest.RoleName != r.RoleName || !latest.ExpiresAt.After(time.Now()) {
		return
	}
	back, e := (&net.Dialer{Timeout: 3 * time.Second}).DialContext(call, "tcp", p.Target)
	if e != nil {
		return
	}
	defer func() { _ = back.Close() }()
	stopBack := context.AfterFunc(ctx, func() { _ = back.Close() })
	defer stopBack()
	_ = back.SetWriteDeadline(time.Now().Add(3 * time.Second))
	if _, e = back.Write(packet); e != nil {
		return
	}
	_ = back.SetWriteDeadline(time.Time{})
	_ = front.SetDeadline(time.Time{})
	done := make(chan struct{})
	go func() { _, _ = io.Copy(back, front); _ = back.Close(); close(done) }()
	_, _ = io.Copy(front, back)
	_ = front.Close()
	<-done
}

func postgresStartup(c net.Conn) ([]byte, error) {
	for tries := 0; tries < 3; tries++ {
		var header [8]byte
		if _, e := io.ReadFull(c, header[:]); e != nil {
			return nil, e
		}
		size := binary.BigEndian.Uint32(header[:4])
		code := binary.BigEndian.Uint32(header[4:])
		if size == 8 && (code == 80877103 || code == 80877104) {
			if _, e := c.Write([]byte{'N'}); e != nil {
				return nil, e
			}
			continue
		}
		if size < 9 || size > 8192 || code != 196608 {
			return nil, errors.New("unsupported PostgreSQL startup")
		}
		packet := make([]byte, int(size))
		copy(packet, header[:])
		_, e := io.ReadFull(c, packet[8:])
		return packet, e
	}
	return nil, errors.New("unsupported PostgreSQL negotiation")
}

func matchingStartup(packet []byte, r Resource) bool {
	if len(packet) < 10 {
		return false
	}
	parts := strings.Split(string(packet[8:]), "\x00")
	if len(parts) < 4 || parts[len(parts)-1] != "" || parts[len(parts)-2] != "" {
		return false
	}
	parts = parts[:len(parts)-2]
	if len(parts)%2 != 0 {
		return false
	}
	values := map[string]string{}
	for i := 0; i < len(parts); i += 2 {
		key := parts[i]
		if _, ok := values[key]; ok {
			return false
		}
		switch key {
		case "user", "database", "application_name", "client_encoding", "DateStyle", "TimeZone", "standard_conforming_strings":
		default:
			return false
		}
		values[key] = parts[i+1]
	}
	return values["user"] == r.RoleName && values["database"] == r.DatabaseName
}
