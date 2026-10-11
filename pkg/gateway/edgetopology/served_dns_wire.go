package edgetopology

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/netip"
	"strconv"
	"strings"

	"github.com/miekg/dns"
	"github.com/onebox-faas/faas/pkg/api"
)

func dnsEndpoint(address string) bool {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return false
	}
	n, err := strconv.ParseUint(port, 10, 16)
	ip, ipErr := netip.ParseAddr(host)
	return err == nil && n != 0 && strconv.FormatUint(n, 10) == port && ipErr == nil && ip.String() == host && !ip.Is4In6() && ip.Zone() == "" && !ip.IsUnspecified() && !ip.IsMulticast() && net.JoinHostPort(host, port) == address
}

func dnsQName(name string) string {
	if name == "." {
		return name
	}
	return name + "."
}

// Each exchange uses a fresh TCP connection to the literal reviewed endpoint,
// RD=false, no EDNS/DO, no proxy/resolver, no UDP fallback, retry or alias chase.
func exchangeServedDNS(ctx context.Context, address string, q DNSQuestion) (*dns.Msg, string, error) {
	if !dnsEndpoint(address) || servedDNSQType(q.Type) == 0 || (q.Name != "." && !dnsName(q.Name, true)) {
		return nil, "", fmt.Errorf("%w: literal endpoint and canonical supported question required", ErrDNSUnverified)
	}
	ctx, cancel := context.WithTimeout(ctx, api.RuntimeUpgradeIngressProbeTimeout)
	defer cancel()
	query := &dns.Msg{MsgHdr: dns.MsgHdr{Id: dns.Id()}, Question: []dns.Question{{Name: dnsQName(q.Name), Qtype: servedDNSQType(q.Type), Qclass: dns.ClassINET}}}
	body, err := query.Pack()
	if err != nil {
		return nil, "", ErrDNSUnverified
	}
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, "", fmt.Errorf("%w: literal DNS endpoint unavailable", ErrDNSUnverified)
	}
	defer func() { _ = conn.Close() }()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	deadline, _ := ctx.Deadline()
	if err := conn.SetDeadline(deadline); err != nil {
		return nil, "", ErrDNSUnverified
	}
	frame := make([]byte, 2+len(body))
	if len(body) > api.RuntimeUpgradeServedDNSWireMaxBytes {
		return nil, "", ErrDNSUnverified
	}
	binary.BigEndian.PutUint16(frame, uint16(len(body)))
	copy(frame[2:], body)
	if n, err := conn.Write(frame); err != nil || n != len(frame) {
		return nil, "", fmt.Errorf("%w: DNS request write failed", ErrDNSUnverified)
	}
	var size [2]byte
	if _, err := io.ReadFull(conn, size[:]); err != nil {
		return nil, "", fmt.Errorf("%w: DNS frame unavailable", ErrDNSUnverified)
	}
	length := int(binary.BigEndian.Uint16(size[:]))
	if length < 12 || length > api.RuntimeUpgradeServedDNSWireMaxBytes {
		return nil, "", fmt.Errorf("%w: bounded DNS frame required", ErrDNSUnverified)
	}
	body = make([]byte, length)
	if _, err := io.ReadFull(conn, body); err != nil {
		return nil, "", fmt.Errorf("%w: incomplete DNS frame", ErrDNSUnverified)
	}
	reply, err := strictDNSMessage(body)
	if err != nil || reply.Id != query.Id || !reply.Response || reply.Opcode != dns.OpcodeQuery || reply.Rcode != dns.RcodeSuccess || reply.Truncated || reply.RecursionDesired || reply.Zero || !strings.EqualFold(reply.Question[0].Name, query.Question[0].Name) || reply.Question[0].Qtype != query.Question[0].Qtype || reply.Question[0].Qclass != dns.ClassINET || ctx.Err() != nil {
		return nil, "", fmt.Errorf("%w: exact successful nonrecursive DNS response required", ErrDNSUnverified)
	}
	return reply, configDigest(body), nil
}

// miekg/dns may adjust dishonest counts or ignore trailing bytes. Walk the wire
// explicitly before accepting its decoded message, with bounded RR counts.
func strictDNSMessage(body []byte) (*dns.Msg, error) {
	if len(body) < 12 || len(body) > api.RuntimeUpgradeServedDNSWireMaxBytes || binary.BigEndian.Uint16(body[4:6]) != 1 {
		return nil, ErrDNSUnverified
	}
	count := int(binary.BigEndian.Uint16(body[6:8])) + int(binary.BigEndian.Uint16(body[8:10])) + int(binary.BigEndian.Uint16(body[10:12]))
	if count > api.RuntimeUpgradeServedDNSRRLimit {
		return nil, ErrDNSUnverified
	}
	_, offset, err := dns.UnpackDomainName(body, 12)
	if err != nil || offset+4 > len(body) {
		return nil, ErrDNSUnverified
	}
	offset += 4
	for i := 0; i < count; i++ {
		_, next, err := dns.UnpackRR(body, offset)
		if err != nil || next <= offset {
			return nil, ErrDNSUnverified
		}
		offset = next
	}
	if offset != len(body) {
		return nil, ErrDNSUnverified
	}
	reply := new(dns.Msg)
	if err := reply.Unpack(body); err != nil || len(reply.Question) != 1 || len(reply.Answer)+len(reply.Ns)+len(reply.Extra) != count {
		return nil, ErrDNSUnverified
	}
	return reply, nil
}

func servedDNSQType(kind string) uint16 {
	if kind == "SOA" {
		return dns.TypeSOA
	}
	if kind == "NS" {
		return dns.TypeNS
	}
	return dnsQuestionType(kind)
}
