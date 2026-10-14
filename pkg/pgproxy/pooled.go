package pgproxy

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"strings"

	"github.com/jackc/pgx/v5/pgproto3"
)

// pooledParams checks a pooled client's startup parameters. Only the role
// and database reach the server: the server is shared, so a client's
// session settings cannot be applied to it. Parameters drivers send for
// their own bookkeeping are accepted when they match the server's defaults;
// anything else is refused with a pointer to the direct endpoint.
func pooledParams(in map[string]string) (map[string]string, error) {
	out := map[string]string{"user": in["user"], "database": in["database"]}
	if out["database"] == "" {
		out["database"] = in["user"]
	}
	for k, v := range in {
		switch k {
		case "user", "database", "application_name", "extra_float_digits":
		case "client_encoding":
			if !strings.EqualFold(v, "UTF8") && !strings.EqualFold(v, "UTF-8") && !strings.EqualFold(v, "UNICODE") {
				return nil, fmt.Errorf("client_encoding %q", v)
			}
		case "DateStyle", "datestyle":
			if !strings.HasPrefix(strings.ToUpper(v), "ISO") {
				return nil, fmt.Errorf("DateStyle %q", v)
			}
		case "options":
			for _, field := range strings.Fields(v) {
				if !strings.HasPrefix(field, "endpoint=") {
					return nil, fmt.Errorf("options %q", v)
				}
			}
		default:
			return nil, fmt.Errorf("%s", k)
		}
	}
	return out, nil
}

// expectation is what the session does with a forwarded message's
// response, in the order the server answers.
type expectation struct {
	// kind is the client message type answered ('P', 'B', 'D', 'E', 'C',
	// 'S', 'Q', 'F').
	kind byte
	// synthetic is sent to the client in place of a server answer.
	synthetic []byte
	// swallow drops the server's answer (a Parse the proxy injected).
	swallow bool
	// prepared is the server statement a forwarded Parse prepares. It is
	// recorded prepared when sent, so later messages of the same batch do
	// not prepare it again, and forgotten if the server refuses it.
	prepared string
}

// pooledSession is one pooled client.
type pooledSession struct {
	p        *Proxy
	pl       *pool
	client   *pgproto3.Backend
	out      *bufio.Writer
	server   *serverConn
	expect   []expectation
	txStatus byte
	// copyIn: the server waits for the client's COPY data.
	copyIn     bool
	statements map[string]*pgproto3.Parse // client name -> its Parse
	events     chan serverEvent
	resume     chan bool
	stop       chan struct{}
	egress     func(int)
}

type serverEvent struct {
	msg []byte
	err error
}

// servePooled runs a pooled client until it leaves.
func (p *Proxy) servePooled(ctx context.Context, conn net.Conn, client *pgproto3.Backend, pl *pool) {
	defer pl.leave()
	egress := p.pools.egressOf(pl.key.databaseID)
	s := &pooledSession{p: p, pl: pl, client: client, out: bufio.NewWriterSize(conn, 32<<10), txStatus: 'I',
		statements: map[string]*pgproto3.Parse{}, egress: func(n int) { egress.Add(int64(n)) }}
	defer s.unlink(false)
	for {
		msg, err := client.Receive()
		if err != nil {
			return
		}
		if _, ok := msg.(*pgproto3.Terminate); ok {
			return
		}
		if err := s.handle(ctx, msg); err != nil {
			s.fail(err)
			return
		}
	}
}

// fail ends the client with an error it can show.
func (s *pooledSession) fail(err error) {
	code, msg := "08006", "the pooled connection to the database failed"
	var f *fatal
	switch {
	case errors.As(err, &f):
		code, msg = f.code, f.msg
	case errors.Is(err, errPoolFull):
		code, msg = "53300", "every pooled connection to the database is busy; retry shortly"
	}
	buf, encErr := (&pgproto3.ErrorResponse{Severity: "FATAL", SeverityUnlocalized: "FATAL", Code: code, Message: msg}).Encode(nil)
	if encErr == nil {
		_, _ = s.out.Write(buf)
		_ = s.out.Flush()
	}
}

// handle forwards one client message, borrowing a server first if the
// client holds none, and relays answers until the client must speak again.
func (s *pooledSession) handle(ctx context.Context, msg pgproto3.FrontendMessage) error {
	if s.server == nil {
		if err := s.link(ctx); err != nil {
			return err
		}
	}
	if err := s.forward(msg); err != nil {
		return err
	}
	if !needsFlush(msg) {
		return nil
	}
	if err := s.server.fe.Flush(); err != nil {
		return err
	}
	return s.relay()
}

// needsFlush reports the messages after which the client waits for
// answers, or that stream data the server must see.
func needsFlush(msg pgproto3.FrontendMessage) bool {
	switch msg.(type) {
	case *pgproto3.Query, *pgproto3.Sync, *pgproto3.Flush, *pgproto3.FunctionCall, *pgproto3.CopyData, *pgproto3.CopyDone, *pgproto3.CopyFail:
		return true
	}
	return false
}

// link borrows a server and starts reading its answers.
func (s *pooledSession) link(ctx context.Context) error {
	srv, err := s.pl.acquire(ctx)
	if err != nil {
		return err
	}
	s.server = srv
	s.events = make(chan serverEvent, 64)
	s.resume = make(chan bool)
	s.stop = make(chan struct{})
	go readServer(srv, s.events, s.resume, s.stop)
	return nil
}

// readServer copies a server's messages out; after each ReadyForQuery it
// waits to be told whether to read on. stop ends it at once.
func readServer(srv *serverConn, events chan<- serverEvent, resume <-chan bool, stop <-chan struct{}) {
	defer close(events)
	send := func(ev serverEvent) bool {
		select {
		case events <- ev:
			return true
		case <-stop:
			return false
		}
	}
	for {
		msg, err := srv.fe.Receive()
		if err != nil {
			send(serverEvent{err: err})
			return
		}
		buf, err := msg.Encode(nil)
		if err != nil {
			send(serverEvent{err: err})
			return
		}
		if !send(serverEvent{msg: buf}) {
			return
		}
		if _, ok := msg.(*pgproto3.ReadyForQuery); ok {
			select {
			case next := <-resume:
				if !next {
					return
				}
			case <-stop:
				return
			}
		}
	}
}

// unlink returns the server: clean only between transactions with every
// answer relayed.
func (s *pooledSession) unlink(clean bool) {
	if s.server == nil {
		return
	}
	srv := s.server
	s.server = nil
	s.expect, s.copyIn = nil, false
	if !clean {
		srv.close()
		close(s.stop)
		for range s.events {
		}
		s.pl.put(srv, false)
		return
	}
	s.resume <- false
	for range s.events {
	}
	s.pl.put(srv, true)
}

// relay forwards server answers until none is owed (the client sent a
// Flush, or the transaction ended), or until the server waits for the
// client's COPY data.
func (s *pooledSession) relay() error {
	for {
		if s.flushSynthetic() {
			continue
		}
		if len(s.expect) == 0 {
			return s.out.Flush()
		}
		if len(s.events) == 0 {
			if err := s.out.Flush(); err != nil {
				return err
			}
			if s.copyIn {
				return nil
			}
		}
		ev, ok := <-s.events
		if !ok || ev.err != nil {
			return errors.Join(errors.New("pgproxy: pooled server ended"), ev.err)
		}
		done, err := s.answer(ev.msg)
		if err != nil {
			return err
		}
		if done {
			return s.out.Flush()
		}
	}
}

// flushSynthetic sends the answers the proxy owes for messages it did not
// forward, once they are next in line.
func (s *pooledSession) flushSynthetic() bool {
	if len(s.expect) == 0 || s.expect[0].synthetic == nil {
		return false
	}
	s.write(s.expect[0].synthetic)
	s.expect = s.expect[1:]
	return true
}

func (s *pooledSession) write(buf []byte) {
	n, _ := s.out.Write(buf)
	s.egress(n)
}

// answer handles one server message; done reports the end of a
// transaction, the server returned.
func (s *pooledSession) answer(buf []byte) (bool, error) {
	kind := buf[0]
	pass := true
	switch kind {
	case '1': // ParseComplete
		pass = !s.pop('P').swallow
	case '2': // BindComplete
		s.pop('B')
	case '3': // CloseComplete
		s.pop('C')
	case 'T', 'n': // RowDescription, NoData
		if len(s.expect) > 0 && s.expect[0].kind == 'D' {
			s.pop('D')
		}
	case 'C', 'I', 's': // CommandComplete, EmptyQueryResponse, PortalSuspended
		s.copyIn = false
		if len(s.expect) > 0 && s.expect[0].kind == 'E' {
			s.pop('E')
		}
	case 'G', 'W': // CopyInResponse, CopyBothResponse
		s.copyIn = true
	case 'E': // ErrorResponse: the server skips to the next Sync.
		s.copyIn = false
		s.skipToSync()
	case 'Z': // ReadyForQuery
		s.pop(0)
		s.txStatus = buf[len(buf)-1]
		s.write(buf)
		if len(s.expect) == 0 && s.txStatus == 'I' {
			s.unlink(true)
			return true, nil
		}
		s.resume <- true
		return false, nil
	}
	if pass {
		s.write(buf)
	}
	return false, nil
}

// pop removes the next expectation, which answers kind (0: any).
func (s *pooledSession) pop(kind byte) expectation {
	if len(s.expect) == 0 {
		return expectation{}
	}
	e := s.expect[0]
	if kind != 0 && e.kind != kind {
		return expectation{}
	}
	s.expect = s.expect[1:]
	return e
}

// skipToSync drops the answers the server will not send after an error in
// an extended-protocol batch: everything up to the next sync point. The
// statements those Parses would have prepared were not.
func (s *pooledSession) skipToSync() {
	for len(s.expect) > 0 {
		e := s.expect[0]
		if e.kind == 'S' || e.kind == 'Q' || e.kind == 'F' {
			return
		}
		if e.prepared != "" && s.server != nil {
			delete(s.server.prepared, e.prepared)
		}
		s.expect = s.expect[1:]
	}
}

// forward sends one client message, rewriting named prepared statements
// to the server's content-addressed names and preparing them on this
// server first when needed.
func (s *pooledSession) forward(msg pgproto3.FrontendMessage) error {
	fe := s.server.fe
	switch m := msg.(type) {
	case *pgproto3.Parse:
		if m.Name == "" {
			fe.Send(m)
			s.expect = append(s.expect, expectation{kind: 'P'})
			return nil
		}
		parse := &pgproto3.Parse{Name: m.Name, Query: m.Query, ParameterOIDs: append([]uint32(nil), m.ParameterOIDs...)}
		s.statements[m.Name] = parse
		name := serverStatement(parse)
		if s.server.prepared[name] {
			s.expect = append(s.expect, expectation{kind: 'P', synthetic: []byte{'1', 0, 0, 0, 4}})
			return nil
		}
		fe.Send(&pgproto3.Parse{Name: name, Query: parse.Query, ParameterOIDs: parse.ParameterOIDs})
		s.server.prepared[name] = true
		s.expect = append(s.expect, expectation{kind: 'P', prepared: name})
	case *pgproto3.Bind:
		name, err := s.prepare(m.PreparedStatement)
		if err != nil {
			return err
		}
		bind := *m
		bind.PreparedStatement = name
		fe.Send(&bind)
		s.expect = append(s.expect, expectation{kind: 'B'})
	case *pgproto3.Describe:
		d := *m
		if m.ObjectType == 'S' {
			name, err := s.prepare(m.Name)
			if err != nil {
				return err
			}
			d.Name = name
		}
		fe.Send(&d)
		s.expect = append(s.expect, expectation{kind: 'D'})
	case *pgproto3.Close:
		if m.ObjectType == 'S' && m.Name != "" {
			// The server keeps the statement for other clients; this
			// client only forgets its name.
			delete(s.statements, m.Name)
			s.expect = append(s.expect, expectation{kind: 'C', synthetic: []byte{'3', 0, 0, 0, 4}})
			return nil
		}
		fe.Send(m)
		s.expect = append(s.expect, expectation{kind: 'C'})
	case *pgproto3.Execute:
		fe.Send(m)
		s.expect = append(s.expect, expectation{kind: 'E'})
	case *pgproto3.Sync:
		fe.Send(m)
		s.expect = append(s.expect, expectation{kind: 'S'})
	case *pgproto3.Query:
		fe.Send(m)
		s.expect = append(s.expect, expectation{kind: 'Q'})
	case *pgproto3.FunctionCall:
		fe.Send(m)
		s.expect = append(s.expect, expectation{kind: 'F'})
	case *pgproto3.CopyDone, *pgproto3.CopyFail:
		// The client is done sending: the server answers now.
		fe.Send(m)
		s.copyIn = false
	case *pgproto3.Flush, *pgproto3.CopyData:
		fe.Send(m)
	default:
		return &fatal{"0A000", fmt.Sprintf("%T is not supported by the pooled endpoint", msg)}
	}
	return nil
}

// prepare returns the server's name for a client statement, preparing it
// on this server if it has not been: a statement may have been parsed on
// another server of the pool. The unnamed statement passes through.
func (s *pooledSession) prepare(clientName string) (string, error) {
	if clientName == "" {
		return "", nil
	}
	parse, ok := s.statements[clientName]
	if !ok {
		return "", &fatal{"26000", fmt.Sprintf("prepared statement %q does not exist", clientName)}
	}
	name := serverStatement(parse)
	if !s.server.prepared[name] {
		s.server.fe.Send(&pgproto3.Parse{Name: name, Query: parse.Query, ParameterOIDs: parse.ParameterOIDs})
		s.server.prepared[name] = true
		s.expect = append(s.expect, expectation{kind: 'P', swallow: true, prepared: name})
	}
	return name, nil
}

// serverStatement names a statement by its text and parameter types, so
// clients preparing the same query share it on a server.
func serverStatement(p *pgproto3.Parse) string {
	h := sha256.New()
	h.Write([]byte(p.Query))
	var oid [4]byte
	for _, o := range p.ParameterOIDs {
		binary.BigEndian.PutUint32(oid[:], o)
		h.Write(oid[:])
	}
	return "gregale_" + hex.EncodeToString(h.Sum(nil)[:12])
}

// greetPooled completes a pooled client's login with the pool's server
// parameters and a backend key of its own (cancel requests are not
// supported).
func greetPooled(client *pgproto3.Backend, out net.Conn, serverFinal []byte, status [][]byte) error {
	client.Send(&pgproto3.AuthenticationSASLFinal{Data: serverFinal})
	client.Send(&pgproto3.AuthenticationOk{})
	if err := client.Flush(); err != nil {
		return err
	}
	for _, buf := range status {
		if _, err := out.Write(buf); err != nil {
			return err
		}
	}
	var key [8]byte
	_, _ = rand.Read(key[:])
	client.Send(&pgproto3.BackendKeyData{ProcessID: binary.BigEndian.Uint32(key[:4]), SecretKey: key[4:]})
	client.Send(&pgproto3.ReadyForQuery{TxStatus: 'I'})
	return client.Flush()
}
