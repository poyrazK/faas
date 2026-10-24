package main

import (
	"flag"
	"io"
	"log"
	"net"
	"net/http"
	"strconv"
)

// Byte-stream fixtures for the instance bridge's session kinds
// (connection-survival ADR P1b), so its metal test can deploy an image:
//
//   - GET /upgrade-echo answers 101 Switching Protocols and echoes every
//     byte that follows, as a WebSocket does over the gateway's raw path;
//   - -udp-echo PORT answers every datagram with itself.
var udpEchoPort = flag.Int("udp-echo", 0, "also serve a UDP echo listener on this port")

func registerStreamFixtures(mux *http.ServeMux) {
	mux.HandleFunc("/upgrade-echo", serveUpgradeEcho)
	if *udpEchoPort > 0 {
		go serveUDPEcho(*udpEchoPort)
	}
}

func serveUpgradeEcho(w http.ResponseWriter, r *http.Request) {
	protocol := r.Header.Get("Upgrade")
	if protocol == "" {
		http.Error(w, "upgrade required", http.StatusUpgradeRequired)
		return
	}
	conn, rw, err := http.NewResponseController(w).Hijack()
	if err != nil {
		http.Error(w, "hijack: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer func() { _ = conn.Close() }()
	_, _ = rw.WriteString("HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: " + protocol + "\r\n\r\n")
	if err := rw.Flush(); err != nil {
		return
	}
	_, _ = io.Copy(conn, rw.Reader)
}

func serveUDPEcho(port int) {
	pc, err := net.ListenPacket("udp", ":"+strconv.Itoa(port))
	if err != nil {
		log.Fatalf("hello-server: UDP echo on :%d: %v", port, err)
	}
	log.Printf("hello-server: UDP echo on :%d", port)
	buf := make([]byte, 64<<10)
	for {
		n, addr, err := pc.ReadFrom(buf)
		if err != nil {
			log.Printf("hello-server: UDP echo read: %v", err)
			continue
		}
		_, _ = pc.WriteTo(buf[:n], addr)
	}
}
