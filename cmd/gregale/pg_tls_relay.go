package main

// A loopback Postgres relay for `gregale dev --local` (ADR-20261107210000).
// Local drivers connect in plaintext to 127.0.0.1; the relay negotiates TLS
// with the database itself, verifying the database's own host name as the
// binding's sslmode asks, over a tunnel that only carries raw bytes. Every
// driver then works without host-name overrides, and plaintext never leaves
// the machine.

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"strconv"
	"sync"
	"time"
)

const (
	pgSSLRequestCode    = 80877103
	pgGSSENCRequestCode = 80877104
)

// pgRelay forwards loopback client connections to upstream (a tunnel to the
// database), adding TLS on the database side.
type pgRelay struct {
	upstream string // host:port of the raw tunnel
	host     string // the database's host name, for SNI and verification
	sslMode  string
	// roots overrides the system roots (tests).
	roots *x509.CertPool
}

// serve accepts until ln closes or ctx ends.
func (r pgRelay) serve(ctx context.Context, ln net.Listener) {
	stop := context.AfterFunc(ctx, func() { _ = ln.Close() })
	defer stop()
	var wg sync.WaitGroup
	defer wg.Wait()
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = r.relay(ctx, conn)
		}()
	}
}

func (r pgRelay) relay(ctx context.Context, client net.Conn) error {
	defer func() { _ = client.Close() }()
	clientReader, err := refuseClientEncryption(client)
	if err != nil {
		return err
	}
	var d net.Dialer
	raw, err := d.DialContext(ctx, "tcp", r.upstream)
	if err != nil {
		return err
	}
	up, err := r.secure(ctx, raw)
	if err != nil {
		_ = raw.Close()
		return err
	}
	defer func() { _ = up.Close() }()
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(up, clientReader); closeWrite(up); done <- struct{}{} }()
	go func() { _, _ = io.Copy(client, up); closeWrite(client); done <- struct{}{} }()
	<-done
	<-done
	return nil
}

// refuseClientEncryption answers 'N' to a client's SSLRequest or
// GSSENCRequest: the loopback leg is plaintext by design. It returns a
// reader positioned at the client's startup message.
func refuseClientEncryption(client net.Conn) (io.Reader, error) {
	br := bufio.NewReader(client)
	for range 2 {
		head, err := br.Peek(8)
		if err != nil {
			return br, nil //nolint:nilerr // a short or closed connection is passed through; the server reports it
		}
		if binary.BigEndian.Uint32(head[:4]) != 8 {
			return br, nil
		}
		code := binary.BigEndian.Uint32(head[4:])
		if code != pgSSLRequestCode && code != pgGSSENCRequestCode {
			return br, nil
		}
		if _, err := br.Discard(8); err != nil {
			return nil, err
		}
		if _, err := client.Write([]byte{'N'}); err != nil {
			return nil, err
		}
	}
	return br, nil
}

// secure negotiates TLS with the database as libpq would for r.sslMode.
func (r pgRelay) secure(ctx context.Context, raw net.Conn) (net.Conn, error) {
	if r.sslMode == "disable" {
		return raw, nil
	}
	_ = raw.SetDeadline(time.Now().Add(30 * time.Second))
	req := make([]byte, 8)
	binary.BigEndian.PutUint32(req[:4], 8)
	binary.BigEndian.PutUint32(req[4:], pgSSLRequestCode)
	if _, err := raw.Write(req); err != nil {
		return nil, err
	}
	answer := make([]byte, 1)
	if _, err := io.ReadFull(raw, answer); err != nil {
		return nil, err
	}
	if answer[0] != 'S' {
		switch r.sslMode {
		case "allow", "prefer":
			_ = raw.SetDeadline(time.Time{})
			return raw, nil
		}
		return nil, fmt.Errorf("the database refused TLS, which sslmode=%s requires", r.sslMode)
	}
	tc := tls.Client(raw, r.tlsConfig())
	if err := tc.HandshakeContext(ctx); err != nil {
		return nil, fmt.Errorf("TLS with %s: %w", r.host, err)
	}
	_ = raw.SetDeadline(time.Time{})
	return tc, nil
}

// tlsConfig follows libpq: verify-full checks the chain and host name,
// verify-ca only the chain, and require/prefer/allow encrypt without
// verifying, as the deployed app's driver does with the same binding.
func (r pgRelay) tlsConfig() *tls.Config {
	cfg := &tls.Config{ServerName: r.host, RootCAs: r.roots, MinVersion: tls.VersionTLS12}
	switch r.sslMode {
	case "verify-full":
		return cfg
	case "verify-ca":
		cfg.InsecureSkipVerify = true //nolint:gosec // verify-ca: the chain is checked below, the host name is not, as in libpq
		cfg.VerifyPeerCertificate = func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
			return verifyChainOnly(rawCerts, r.roots)
		}
		return cfg
	}
	cfg.InsecureSkipVerify = true //nolint:gosec // require/prefer/allow encrypt without verification, matching libpq and the deployed app
	return cfg
}

func verifyChainOnly(rawCerts [][]byte, roots *x509.CertPool) error {
	if len(rawCerts) == 0 {
		return errors.New("the database sent no certificate")
	}
	certs := make([]*x509.Certificate, 0, len(rawCerts))
	for _, raw := range rawCerts {
		cert, err := x509.ParseCertificate(raw)
		if err != nil {
			return err
		}
		certs = append(certs, cert)
	}
	inter := x509.NewCertPool()
	for _, c := range certs[1:] {
		inter.AddCert(c)
	}
	_, err := certs[0].Verify(x509.VerifyOptions{Roots: roots, Intermediates: inter})
	return err
}

func closeWrite(c net.Conn) {
	switch t := c.(type) {
	case *net.TCPConn:
		_ = t.CloseWrite()
	case *tls.Conn:
		_ = t.CloseWrite()
	default:
		_ = c.Close()
	}
}

// localPostgresURL is the binding's URL pointed at the loopback relay, with
// TLS off on that leg and every other parameter kept.
func localPostgresURL(target postgresConnectTarget, port int) string {
	params := url.Values{}
	for k, v := range target.params {
		switch k {
		case "sslmode", "sslrootcert", "sslcert", "sslkey", "sslnegotiation":
			continue
		}
		params[k] = v
	}
	params.Set("sslmode", "disable")
	u := url.URL{
		Scheme:   "postgresql",
		User:     url.UserPassword(target.user, target.password),
		Host:     net.JoinHostPort("127.0.0.1", strconv.Itoa(port)),
		Path:     "/" + target.database,
		RawQuery: params.Encode(),
	}
	return u.String()
}
