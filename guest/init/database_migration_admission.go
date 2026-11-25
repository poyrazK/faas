package main

import (
	"encoding/binary"
	"io"
	"net"
	"slices"
	"time"

	"github.com/jackc/pgx/v5/pgproto3"
)

// migrationAdmission admits only the unpublished cutover credentials so
// the existing SQL verifier can authenticate while application logins
// remain fenced. PostgreSQL still performs SCRAM on the forwarded startup.
type migrationAdmission struct {
	database string
	roles    []string
}

func (a *migrationAdmission) startup(conn net.Conn) []byte {
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	defer conn.SetReadDeadline(time.Time{})
	var header [4]byte
	if _, err := io.ReadFull(conn, header[:]); err != nil {
		return nil
	}
	length := binary.BigEndian.Uint32(header[:])
	if length < 9 || length > 10*1024 {
		return nil
	}
	raw := make([]byte, length-4)
	if _, err := io.ReadFull(conn, raw); err != nil {
		return nil
	}
	var msg pgproto3.StartupMessage
	if err := msg.Decode(raw); err != nil || msg.ProtocolVersion != pgproto3.ProtocolVersionNumber {
		return nil
	}
	p := msg.Parameters
	if p["database"] != a.database || !slices.Contains(a.roles, p["user"]) || p["replication"] != "" {
		return nil
	}
	// Re-encode the validated map so PostgreSQL sees the same role and
	// database even when a malformed client supplied duplicate keys.
	encoded, err := msg.Encode(nil)
	if err != nil {
		return nil
	}
	return encoded
}
