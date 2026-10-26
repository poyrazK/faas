//go:build linux

package main

import (
	"errors"
	"io"
	"log/slog"
	"net"
	"strconv"
	"time"

	"golang.org/x/sys/unix"

	"github.com/onebox-faas/faas/pkg/api"
)

const (
	// devWatchBridgeWindow bounds the wait for a watch-mode development
	// server to listen; a cold Next.js or Nuxt start can take minutes.
	devWatchBridgeWindow = 5 * time.Minute
	devWatchBridgePoll   = 250 * time.Millisecond
	// devWatchBridgeSettle waits for the server's listeners to stop
	// changing, so the bridge never takes a port the server opens next.
	devWatchBridgeSettle = time.Second
)

// runDevWatchBridge makes a watch-mode development server reachable on the
// runtime port (devWatchBridgeTarget). It returns at once for every other
// image, and after it has bridged, found the server reachable, or waited
// devWatchBridgeWindow.
func runDevWatchBridge(appPID func() int, want int, log *slog.Logger) {
	if want <= 0 {
		want = api.DefaultAppPort
	}
	var last []listenSocket
	var stableSince time.Time
	for deadline := time.Now().Add(devWatchBridgeWindow); time.Now().Before(deadline); time.Sleep(devWatchBridgePoll) {
		pid := appPID()
		if pid <= 0 {
			continue
		}
		// Set before the workload starts, so it is final once a PID exists.
		if !devWatchActive.Load() {
			return
		}
		owned := ownedSocketInodes(pid)
		current := append(scanListenSockets("/proc/net/tcp", owned), scanListenSockets("/proc/net/tcp6", owned)...)
		if len(current) == 0 || !sameListeners(current, last) {
			last, stableSince = current, time.Now()
			continue
		}
		if time.Since(stableSince) < devWatchBridgeSettle {
			continue
		}
		guestIP := guestIPv4()
		target, ok := devWatchBridgeTarget(want, guestIP, current)
		if !ok {
			log.Info("dev watch: the development server listens on the runtime port", "port", want)
			return
		}
		if _, err := startDevWatchBridge(guestIP, want, target, log); err != nil {
			log.Warn("dev watch: cannot bridge the runtime port; the development server is unreachable", "port", want, "target", target, "err", err)
			return
		}
		log.Info("dev watch: bridging the runtime port to the development server", "port", want, "target", target)
		return
	}
	log.Warn("dev watch: the development server did not listen", "window", devWatchBridgeWindow)
}

// startDevWatchBridge listens on guestIP:port and proxies every connection
// to target. Binding the guest address, not every interface, leaves the
// port free on loopback for a server that listens there.
func startDevWatchBridge(guestIP net.IP, port int, target string, log *slog.Logger) (net.Listener, error) {
	ln, err := net.Listen("tcp", net.JoinHostPort(guestIP.String(), strconv.Itoa(port)))
	if err != nil {
		return nil, err
	}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				log.Debug("dev watch bridge: accept ended", "err", err)
				return
			}
			go proxyDevWatchConn(c, target, log)
		}
	}()
	return ln, nil
}

// proxyDevWatchConn copies both directions and passes a half-close on, so a
// long-lived HMR websocket stays open and a finished request ends cleanly.
func proxyDevWatchConn(c net.Conn, target string, log *slog.Logger) {
	defer func() { _ = c.Close() }()
	upstream, err := net.DialTimeout("tcp", target, 5*time.Second)
	if err != nil {
		log.Debug("dev watch bridge: dial failed", "target", target, "err", err)
		return
	}
	defer func() { _ = upstream.Close() }()
	done := make(chan struct{})
	go func() {
		_, _ = ioCopy(upstream, c)
		if tcp, ok := upstream.(*net.TCPConn); ok {
			_ = tcp.CloseWrite()
		}
		close(done)
	}()
	_, _ = ioCopy(c, upstream)
	_ = c.Close()
	<-done
}

// ioCopy treats a peer closing the connection (EPIPE) or the listener
// closing as a normal end, so the bridge does not log every finished
// connection as an error.
func ioCopy(dst io.Writer, src io.Reader) (int64, error) {
	const buf = 64 * 1024
	n, err := io.CopyBuffer(dst, src, make([]byte, buf))
	if errors.Is(err, unix.EPIPE) || errors.Is(err, net.ErrClosed) {
		return n, nil
	}
	return n, err
}

// guestIPv4 is the guest's tap address: 10.0.0.2 in every guest (ADR-009),
// read from the interface so the bridge follows the network it is given.
func guestIPv4() net.IP {
	ifaces, _ := net.Interfaces()
	for _, iface := range ifaces {
		if iface.Flags&net.FlagLoopback != 0 || iface.Flags&net.FlagUp == 0 {
			continue
		}
		addrs, _ := iface.Addrs()
		for _, a := range addrs {
			if ipNet, ok := a.(*net.IPNet); ok && ipNet.IP.To4() != nil {
				return ipNet.IP.To4()
			}
		}
	}
	return net.IPv4(10, 0, 0, 2)
}
