//go:build metal

// adr: 20261017201005
package fcvm

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/miekg/dns"
	"github.com/onebox-faas/faas/pkg/fcvm/leakcheck"
	"github.com/onebox-faas/faas/pkg/netns"
)

type metalRehearsalPolicies struct{ appID string }

func (p metalRehearsalPolicies) ScenarioRehearsalNetworkRun(_ context.Context, appID string) (string, error) {
	if p.appID == appID {
		return rehearsalNetworkTestRun, nil
	}
	return "", nil
}

// Explicit opt-in keeps ordinary metal runs independent of an external probe
// endpoint. The acceptance script requires a native host and an operator-owned
// public IP with TCP 80 and 443 open; the guest sends only connect probes.
func TestMetalRehearsalNetworkGuestColdBootAndResume(t *testing.T) {
	if os.Getenv("FAAS_TEST_REHEARSAL_NETWORK") != "1" {
		t.Skip("run the rehearsal-network native acceptance gate")
	}
	kernel, _, _ := metalImages(t)
	public, err := netip.ParseAddr(os.Getenv("FAAS_TEST_REHEARSAL_PUBLIC_IP"))
	if err != nil || !public.Is4() || !public.IsGlobalUnicast() || public.IsPrivate() {
		t.Fatal("FAAS_TEST_REHEARSAL_PUBLIC_IP must be an operator-owned public IPv4 probe endpoint")
	}
	withCgroupRootAt(t, "/sys/fs/cgroup")
	containedApp := uuid.NewString()
	m := newMetalManager(t, kernel).WithRehearsalNetworkPolicies(metalRehearsalPolicies{appID: containedApp})
	// Dedicated acceptance host only: a mock private dependency must own the
	// canonical proxy port. Never substitute an occupied production listener.
	l, err := net.Listen("tcp", net.JoinHostPort(netns.DefaultHostBridgeIP.String(), "10081"))
	if err != nil {
		t.Fatalf("bind isolated test dependency: %v", err)
	}
	s := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("private dependency")) }), ReadHeaderTimeout: time.Second}
	go func() { _ = s.Serve(l) }()
	t.Cleanup(func() { _ = s.Close() })
	for _, port := range []int{netns.ServiceDiscoveryDNSPort, netns.RehearsalServiceDiscoveryDNSPort} {
		packet, err := net.ListenPacket("udp", net.JoinHostPort(netns.DefaultHostBridgeIP.String(), fmt.Sprint(port)))
		if err != nil {
			t.Fatalf("bind isolated DNS fixture: %v", err)
		}
		restricted := port == netns.RehearsalServiceDiscoveryDNSPort
		server := &dns.Server{PacketConn: packet, Handler: dns.HandlerFunc(func(w dns.ResponseWriter, req *dns.Msg) {
			msg := new(dns.Msg)
			msg.SetReply(req)
			for _, question := range req.Question {
				if restricted && question.Name != "worker.svc.gregale." {
					msg.Rcode = dns.RcodeRefused
					break
				}
				if question.Qtype == dns.TypeA {
					msg.Answer = append(msg.Answer, &dns.A{Hdr: dns.RR_Header{Name: question.Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 1}, A: net.IP(netns.DefaultHostBridgeIP.AsSlice())})
				}
			}
			_ = w.WriteMsg(msg)
		})}
		go func() { _ = server.ActivateAndServe() }()
		t.Cleanup(func() { _ = server.Shutdown() })
	}
	script := fmt.Sprintf(`#!/bin/sh
busybox mount -t tmpfs tmpfs /tmp
busybox mkdir -p /tmp/www/cgi-bin
busybox cat > /tmp/www/cgi-bin/probe <<'PROBE'
#!/bin/sh
printf 'Content-Type: text/plain\r\n\r\n'
for port in 80 443; do
  busybox nc -z -w 2 %s "$port" >/dev/null 2>&1
  printf 'port=%%s rc=%%s\n' "$port" "$?"
done
busybox wget -q -T 2 -O /tmp/dependency http://%s:10081/rehearsal >/dev/null 2>&1
printf 'dependency=%%s\n' "$?"
busybox nslookup worker.svc.gregale %s >/dev/null 2>&1
printf 'private_dns=%%s\n' "$?"
busybox nslookup external.example.test %s >/dev/null 2>&1
printf 'public_dns=%%s\n' "$?"
PROBE
busybox chmod 755 /tmp/www/cgi-bin/probe
exec busybox httpd -f -p 8080 -h /tmp/www
`, public.String(), netns.DefaultHostBridgeIP.String(), netns.DefaultHostBridgeIP.String(), netns.DefaultHostBridgeIP.String())
	for _, contained := range []bool{false, true} {
		label := "control"
		appID := uuid.NewString()
		if contained {
			label, appID = "contained", containedApp
		}
		t.Run(label, func(t *testing.T) {
			image := ensureEgressGuestExt4WithScript(t, t.TempDir(), script)
			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
			defer cancel()
			id := uuid.NewString()
			inst, err := m.ColdBoot(ctx, ColdBootRequest{Instance: id, AppID: appID, Plan: "hobby", BaseKey: image, LayerKey: image, VcpuCount: 2, MemSizeMiB: 128, EgressAllowlist: []string{public.String() + "/32"}})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				cleanup, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), 30*time.Second)
				defer cancel()
				if err := m.Destroy(cleanup, id); err != nil {
					t.Error(err)
				}
				leakcheck.AssertZero(t)
			})
			check := func() {
				t.Helper()
				client := &http.Client{Timeout: 15 * time.Second}
				response, err := client.Get("http://" + inst.Lease.HostIP.String() + ":8080/cgi-bin/probe")
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = response.Body.Close() }()
				data, err := io.ReadAll(response.Body)
				if err != nil {
					t.Fatal(err)
				}
				body := string(data)
				for _, port := range []int{80, 443} {
					allowed := strings.Contains(body, fmt.Sprintf("port=%d rc=0", port))
					if allowed == contained {
						t.Fatalf("public TCP %d containment=%v: %s", port, contained, body)
					}
				}
				if !strings.Contains(body, "dependency=0") {
					t.Fatalf("isolated private dependency blocked: %s", body)
				}
				if !strings.Contains(body, "private_dns=0") {
					t.Fatalf("private DNS unavailable: %s", body)
				}
				if publicDNS := strings.Contains(body, "public_dns=0"); publicDNS == contained {
					t.Fatalf("public DNS containment=%v: %s", contained, body)
				}
			}
			check()
			if contained {
				if _, err := m.Freeze(ctx, id, FreezeOptions{}); err != nil {
					t.Fatal(err)
				}
				if err := m.Thaw(ctx, id); err != nil {
					t.Fatal(err)
				}
				check()
			}
		})
	}
}
