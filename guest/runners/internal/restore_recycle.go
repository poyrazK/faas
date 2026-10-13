package internal

import (
	"bufio"
	"fmt"
	"log"
	"net"
	"os"
	"strings"
)

// RegisterRestoreRecycle joins guest-init's restore reseed barrier
// (GHSA-24j2-p895-mwc9, ADR-687) on behalf of handler processes that have no
// reseed preload of their own, such as compiled persistent handlers. On every
// resume guest-init sends a nonce; recycle must stop every handler process
// that was running at capture, and only then does the runner confirm. A
// restored handler would otherwise resume with the in-process random state it
// held at capture, shared with every sibling restored from the same snapshot.
//
// It returns false when the barrier is not available (no socket in the
// environment, or the workload opted out), which leaves handlers unchanged.
func RegisterRestoreRecycle(runtime string, recycle func() error) bool {
	socket := os.Getenv("GREGALE_RESEED_SOCKET")
	if socket == "" || strings.EqualFold(os.Getenv("GREGALE_RESTORE_RESEED"), "off") {
		return false
	}
	conn, err := net.Dial("unix", socket)
	if err != nil {
		log.Printf("%s runner: restore reseed barrier unavailable: %v", runtime, err)
		return false
	}
	if _, err := fmt.Fprintf(conn, "hello %s %d\n", runtime, os.Getpid()); err != nil {
		_ = conn.Close()
		log.Printf("%s runner: restore reseed hello: %v", runtime, err)
		return false
	}
	go serveRestoreRecycle(conn, recycle)
	return true
}

func serveRestoreRecycle(conn net.Conn, recycle func() error) {
	defer func() { _ = conn.Close() }()
	reader := bufio.NewReader(conn)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		if !strings.HasPrefix(strings.TrimSpace(line), "reseed ") {
			continue
		}
		reply := "ok"
		if err := recycle(); err != nil {
			reply = "err recycle"
		}
		if _, err := fmt.Fprintln(conn, reply); err != nil {
			return
		}
	}
}
