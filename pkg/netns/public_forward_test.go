package netns

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func loopbackPublicTarget(t *testing.T) PublicForwardTarget {
	t.Helper()
	iface, err := net.InterfaceByName("lo")
	if err != nil {
		// Portable runners may use lo0; find the loopback by its flags.
		interfaces, listErr := net.Interfaces()
		if listErr != nil {
			t.Fatal(listErr)
		}
		for _, candidate := range interfaces {
			if candidate.Flags&net.FlagLoopback != 0 {
				iface = &candidate
				break
			}
		}
	}
	if iface == nil {
		t.Fatal("no loopback interface")
	}
	return PublicForwardTarget{HostIP: netip.MustParseAddr("127.0.0.1"), BridgeIP: netip.MustParseAddr("127.0.0.1"), Veth: iface.Name, IfIndex: iface.Index, AppPort: AppPort}
}

func TestPublicForwardTargetPortPermutation(t *testing.T) {
	for _, mainPort := range []int{80, 8080, 9000, 65535} {
		target := PublicForwardTarget{AppPort: mainPort}
		seen := make(map[int]bool)
		for guest := 1; guest <= 65535; guest++ {
			published := target.PublishedPort(guest)
			if published < 1 || published > 65535 || seen[published] {
				t.Fatalf("main=%d guest=%d colliding public port=%d", mainPort, guest, published)
			}
			seen[published] = true
		}
		if target.PublishedPort(mainPort) != AppPort || target.PublishedPort(AppPort) != mainPort {
			t.Fatalf("main=%d loses main or :8080 sidecar", mainPort)
		}
	}
}

func TestPublicForwardDialChecksOriginalInterface(t *testing.T) {
	if runtime.GOOS != "linux" || os.Geteuid() != 0 {
		t.Skip("public output pin is a native Linux capability")
	}
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = io.WriteString(w, "streamed through public target")
	}))
	defer srv.Close()
	address := strings.TrimPrefix(srv.URL, "http://")
	target := loopbackPublicTarget(t)
	client := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		return target.DialContext(ctx, &net.Dialer{Timeout: time.Second}, network, address)
	}}}
	defer client.CloseIdleConnections()
	response, err := client.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil || string(body) != "streamed through public target" {
		t.Fatalf("public body: %q %v", body, err)
	}
	target.IfIndex++
	if conn, err := target.DialContext(t.Context(), &net.Dialer{Timeout: time.Second}, "tcp", address); err == nil {
		_ = conn.Close()
		t.Fatal("retired interface connected")
	}
	if requests.Load() != 1 {
		t.Fatalf("stale target sent %d requests", requests.Load())
	}
}

func TestPublicForwardRetirementDuringConnectClosesSocket(t *testing.T) {
	if runtime.GOOS != "linux" || os.Geteuid() != 0 {
		t.Skip("public output pin is a native Linux capability")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	read := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			read <- err
			return
		}
		defer func() { _ = conn.Close() }()
		_ = conn.SetReadDeadline(time.Now().Add(time.Second))
		_, err = conn.Read(make([]byte, 1))
		read <- err
	}()
	target := loopbackPublicTarget(t)
	checks := 0
	conn, err := target.dialContext(t.Context(), &net.Dialer{Timeout: time.Second}, "tcp", listener.Addr().String(), func(string) (*net.Interface, error) {
		checks++
		return &net.Interface{Index: target.IfIndex + checks - 1}, nil
	})
	if err == nil || conn != nil || checks != 2 {
		t.Fatalf("retirement race: conn=%v err=%v checks=%d", conn, err, checks)
	}
	if err := <-read; !errors.Is(err, io.EOF) {
		t.Fatalf("recycled socket received data or leaked: %v", err)
	}
}

func TestPublicForwardEnvFailsClosedAndClearsPrivateAuthority(t *testing.T) {
	target := loopbackPublicTarget(t)
	for _, value := range []string{"", "{}", "not json"} {
		t.Setenv(publicForwardEnv, value)
		if conn, err := DialBridgeContext(t.Context(), &net.Dialer{}, "tcp", "10.0.0.2:8080"); err == nil || conn != nil {
			t.Fatal("invalid public authority fell back to guest dial")
		}
	}
	env := PublicForwardEnv([]string{"KEEP=yes", publicForwardEnv + "=stale", publicForwardEnv + "=duplicate"}, &target)
	if len(env) != 2 || env[0] != "KEEP=yes" {
		t.Fatalf("replacement env: %v", env)
	}
	var decoded PublicForwardTarget
	if err := json.Unmarshal([]byte(strings.TrimPrefix(env[1], publicForwardEnv+"=")), &decoded); err != nil || decoded != target {
		t.Fatalf("target identity: %+v %v", decoded, err)
	}
	if private := PublicForwardEnv(env, nil); len(private) != 1 || private[0] != "KEEP=yes" {
		t.Fatalf("private helper inherited public route: %v", private)
	}
	if _, ok := os.LookupEnv(publicForwardEnv); !ok {
		t.Fatal("test authority unexpectedly absent")
	}
}

func TestPublicForwardNodeOnlyDNATAndRetarget(t *testing.T) {
	c := NewConfig("i", "fc-i", "vh-i", "vp-i", netip.MustParseAddr("10.100.0.2"))
	c.PublicForwarding = true
	c.GuestAppPort = 9000
	var lines []string
	for _, cmd := range c.RetargetAppPortCommands() {
		lines = append(lines, strings.Join(cmd, " "))
	}
	joined := strings.Join(lines, "\n")
	want := []string{
		"iifname vp-i ip saddr 10.100.0.1 ip daddr 10.100.0.2 tcp dport 9000 dnat to 10.0.0.2:8080",
		"iifname vp-i tcp dport 8080 dnat to 10.0.0.2:9000",
		"iifname vp-i ip saddr 10.100.0.1 ip daddr 10.100.0.2 meta l4proto { tcp,udp } dnat to 10.0.0.2",
	}
	last := -1
	for _, fragment := range want {
		index := strings.Index(joined, fragment)
		if index <= last {
			t.Fatalf("DNAT ordering lost %q:\n%s", fragment, joined)
		}
		last = index
	}
	for _, cmd := range c.NftCommands() {
		line := strings.Join(cmd, " ")
		if strings.Contains(line, "prerouting iifname vp-i") && !strings.Contains(joined, line) {
			t.Fatalf("retarget lost public rule: %s", line)
		}
	}
	foundRoute := false
	for _, cmd := range c.SetupCommands() {
		if strings.Join(cmd, " ") == "ip route add 10.100.0.2/32 dev vh-i scope link src 10.100.0.1" {
			foundRoute = true
		}
	}
	if !foundRoute {
		t.Fatal("pinned sockets lack an original-device host route")
	}
}
