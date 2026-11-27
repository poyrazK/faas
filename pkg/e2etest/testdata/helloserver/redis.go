package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// GET /redis?host=&port=&cmd=SET+k+v&cmd=GET+k opens one connection from
// inside the guest, sends every cmd (space-separated arguments) as a Redis
// command, and reports one raw reply per command as JSON. It is the managed
// Redis metal fixture's client (ADR-20261116192126); one connection per
// request, like a short-lived client.

type redisDialResult struct {
	Replies []string `json:"replies"`
	Error   string   `json:"error,omitempty"`
}

const redisDialTimeout = 2 * time.Minute

func registerRedisFixture(mux *http.ServeMux) {
	mux.HandleFunc("/redis", serveRedisDial)
}

func serveRedisDial(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	var res redisDialResult
	status := redisDialAndRead(q.Get("host"), q.Get("port"), q["cmd"], &res)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(res)
}

func redisDialAndRead(host, port string, cmds []string, res *redisDialResult) int {
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, port), redisDialTimeout)
	if err != nil {
		res.Error = err.Error()
		return http.StatusBadGateway
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(redisDialTimeout))
	var out strings.Builder
	for _, cmd := range cmds {
		args := strings.Fields(cmd)
		fmt.Fprintf(&out, "*%d\r\n", len(args))
		for _, a := range args {
			fmt.Fprintf(&out, "$%d\r\n%s\r\n", len(a), a)
		}
	}
	if _, err := io.WriteString(conn, out.String()); err != nil {
		res.Error = err.Error()
		return http.StatusBadGateway
	}
	reader := bufio.NewReader(conn)
	for range cmds {
		var reply strings.Builder
		if err := readRedisValue(reader, &reply, 0); err != nil {
			res.Error = err.Error()
			return http.StatusBadGateway
		}
		res.Replies = append(res.Replies, reply.String())
	}
	return http.StatusOK
}

// readRedisValue copies one RESP2/RESP3 value, CRLFs included.
func readRedisValue(r *bufio.Reader, out *strings.Builder, depth int) error {
	if depth > 32 {
		return fmt.Errorf("reply nested too deep")
	}
	line, err := r.ReadString('\n')
	if err != nil {
		return err
	}
	out.WriteString(line)
	line = strings.TrimRight(line, "\r\n")
	if line == "" {
		return fmt.Errorf("empty reply line")
	}
	n, _ := strconv.Atoi(line[1:])
	switch line[0] {
	case '$', '!', '=':
		if n < 0 {
			return nil
		}
		buf := make([]byte, n+2)
		if _, err := io.ReadFull(r, buf); err != nil {
			return err
		}
		out.Write(buf)
	case '*', '~', '>':
		for i := 0; i < n; i++ {
			if err := readRedisValue(r, out, depth+1); err != nil {
				return err
			}
		}
	case '%', '|':
		for i := 0; i < 2*n; i++ {
			if err := readRedisValue(r, out, depth+1); err != nil {
				return err
			}
		}
	}
	return nil
}
