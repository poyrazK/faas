// Command financialguest is an isolated native-acceptance workload. It is
// built into the disposable two-drive guest fixture, never a platform daemon.
package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

const payloadSize = 64 * 1024

func payload(port int) []byte { return bytes.Repeat([]byte{byte(port % 251)}, payloadSize) }

func streamResponse(port int) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/upgrade" {
			upgrade(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("X-Financial-Guest-Port", fmt.Sprint(port))
		body := payload(port)
		for len(body) > 0 {
			n := min(len(body), 4096)
			if _, err := w.Write(body[:n]); err != nil {
				return
			}
			if err := http.NewResponseController(w).Flush(); err != nil {
				return
			}
			body = body[n:]
			time.Sleep(time.Millisecond)
		}
	})
}

func upgrade(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Upgrade") != "financial-echo" {
		http.Error(w, "upgrade required", http.StatusBadRequest)
		return
	}
	conn, buffered, err := http.NewResponseController(w).Hijack()
	if err != nil {
		return
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
	if _, err := buffered.WriteString("HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: financial-echo\r\n\r\n"); err != nil {
		return
	}
	if err := buffered.Flush(); err != nil {
		return
	}
	_, _ = io.CopyN(conn, buffered, payloadSize)
}

type payloadService interface {
	Read(context.Context, *emptypb.Empty) (*wrapperspb.BytesValue, error)
}
type guestPayload struct{}

func (guestPayload) Read(context.Context, *emptypb.Empty) (*wrapperspb.BytesValue, error) {
	return wrapperspb.Bytes(payload(9002)), nil
}

func payloadRead(server any, ctx context.Context, decode func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
	req := new(emptypb.Empty)
	if err := decode(req); err != nil {
		return nil, err
	}
	handler := func(ctx context.Context, req any) (any, error) {
		return server.(payloadService).Read(ctx, req.(*emptypb.Empty))
	}
	if interceptor != nil {
		return interceptor(ctx, req, &grpc.UnaryServerInfo{Server: server, FullMethod: "/financial.fixture.Payload/Read"}, handler)
	}
	return handler(ctx, req)
}

func grpcHandler() http.Handler {
	server := grpc.NewServer()
	server.RegisterService(&grpc.ServiceDesc{ServiceName: "financial.fixture.Payload", HandlerType: (*payloadService)(nil), Methods: []grpc.MethodDesc{{MethodName: "Read", Handler: payloadRead}}}, guestPayload{})
	return server
}

func guestProtocols() *http.Protocols {
	protocols := new(http.Protocols)
	protocols.SetHTTP1(true)
	protocols.SetUnencryptedHTTP2(true)
	return protocols
}

func echoTCP(listener net.Listener) error {
	for {
		conn, err := listener.Accept()
		if err != nil {
			return err
		}
		go func() {
			defer func() { _ = conn.Close() }()
			_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
			_, _ = io.CopyN(conn, conn, payloadSize)
		}()
	}
}

func echoUDP(conn net.PacketConn) error {
	buffer := make([]byte, 65507)
	for {
		n, peer, err := conn.ReadFrom(buffer)
		if err != nil {
			return err
		}
		if _, err := conn.WriteTo(buffer[:n], peer); err != nil {
			return err
		}
	}
}

func run() error {
	failed := make(chan error, 6)
	for _, port := range []int{9000, 8080, 9001, 9002} {
		listener, err := net.Listen("tcp4", fmt.Sprintf("10.0.0.2:%d", port))
		if err != nil {
			return err
		}
		defer func() { _ = listener.Close() }()
		handler := streamResponse(port)
		if port == 9002 {
			handler = grpcHandler()
		}
		server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, Protocols: guestProtocols()}
		go func() { failed <- server.Serve(listener) }()
	}
	tcp, err := net.Listen("tcp4", "10.0.0.2:9090")
	if err != nil {
		return err
	}
	defer func() { _ = tcp.Close() }()
	go func() { failed <- echoTCP(tcp) }()
	udp, err := net.ListenPacket("udp4", "10.0.0.2:9091")
	if err != nil {
		return err
	}
	defer func() { _ = udp.Close() }()
	go func() { failed <- echoUDP(udp) }()
	return <-failed
}

func main() {
	log.SetOutput(os.Stderr)
	if err := run(); err != nil {
		log.Fatal(err)
	}
}
