package main

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

func TestStreamedResponseIdentifiesActualPort(t *testing.T) {
	for _, port := range []int{9000, 8080, 9001} {
		server := httptest.NewServer(streamResponse(port))
		client := server.Client()
		client.Timeout = 5 * time.Second
		response, err := client.Get(server.URL)
		if err != nil {
			server.Close()
			t.Fatal(err)
		}
		body, err := io.ReadAll(response.Body)
		_ = response.Body.Close()
		server.Close()
		if err != nil || !bytes.Equal(body, payload(port)) || len(response.TransferEncoding) != 1 || response.TransferEncoding[0] != "chunked" {
			t.Fatalf("port %d: bytes=%d transfer=%v error=%v", port, len(body), response.TransferEncoding, err)
		}
	}
}

func TestGRPCPayloadOverH2C(t *testing.T) {
	server := httptest.NewUnstartedServer(grpcHandler())
	server.Config.Protocols = guestProtocols()
	server.Start()
	defer server.Close()
	client, err := grpc.NewClient("passthrough:///"+server.Listener.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	var result wrapperspb.BytesValue
	if err := client.Invoke(ctx, "/financial.fixture.Payload/Read", &emptypb.Empty{}, &result); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(result.Value, payload(9002)) {
		t.Fatal("gRPC payload identity/size changed")
	}
}

func TestUpgradeCarriesBinaryPayload(t *testing.T) {
	server := httptest.NewServer(streamResponse(9000))
	defer server.Close()
	conn, err := net.DialTimeout("tcp", server.Listener.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	_, err = io.WriteString(conn, "GET /upgrade HTTP/1.1\r\nHost: fixture\r\nConnection: Upgrade\r\nUpgrade: financial-echo\r\n\r\n")
	if err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(conn)
	response, err := http.ReadResponse(reader, &http.Request{Method: http.MethodGet})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != 101 {
		t.Fatal(response.StatusCode)
	}
	body := bytes.Repeat([]byte{0, 1, 2, 255}, payloadSize/4)
	if _, err := conn.Write(body); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, len(body))
	if _, err := io.ReadFull(reader, got); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, body) {
		t.Fatal("upgrade binary echo changed")
	}
}

func TestTCPEcho(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- echoTCP(listener) }()
	defer func() { _ = listener.Close(); <-done }()
	conn, err := net.DialTimeout("tcp4", listener.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	body := payload(9090)
	if _, err := conn.Write(body); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, len(body))
	_, err = io.ReadFull(conn, got)
	if err != nil || !bytes.Equal(got, body) {
		t.Fatalf("TCP echo: %v", err)
	}
}

func TestUDPEcho(t *testing.T) {
	listener, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- echoUDP(listener) }()
	defer func() { _ = listener.Close(); <-done }()
	conn, err := net.DialTimeout("udp4", listener.LocalAddr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	body := bytes.Repeat([]byte{0, 255}, 2048)
	if _, err := conn.Write(body); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, len(body))
	n, err := conn.Read(got)
	if err != nil || !bytes.Equal(got[:n], body) {
		t.Fatalf("UDP echo: %v", err)
	}
}
