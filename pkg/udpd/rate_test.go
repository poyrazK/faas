package udpd

import (
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestAccountRateLimitsEmptyPacketsAndRefills(t *testing.T) {
	limits := NewRateLimits()
	now := time.Unix(100, 0)
	for i := 0; i < api.UDPPacketBurstPerAccount; i++ {
		if !limits.allowAt("a", 0, false, now) {
			t.Fatalf("burst denied at %d", i)
		}
	}
	if limits.allowAt("a", 0, false, now) {
		t.Fatal("empty datagrams bypassed packet cap")
	}
	if !limits.allowAt("a", 0, true, now) || !limits.allowAt("b", 0, false, now) {
		t.Fatal("independent direction/account shared the exhausted bucket")
	}
	if !limits.allowAt("a", 0, false, now.Add(time.Second/time.Duration(api.UDPPacketsPerSecondPerAccount))) {
		t.Fatal("packet credit did not refill")
	}
}
func TestAccountRateLimitsPayloadBytes(t *testing.T) {
	limits := NewRateLimits()
	now := time.Unix(100, 0)
	for i := 0; i < api.UDPByteBurstPerAccount/api.UDPDatagramMaxBytes; i++ {
		if !limits.allowAt("a", api.UDPDatagramMaxBytes, false, now) {
			t.Fatal("byte burst denied early")
		}
	}
	if limits.allowAt("a", 1, false, now) {
		t.Fatal("byte cap bypassed")
	}
	if !limits.allowAt("a", api.UDPDatagramMaxBytes, false, now.Add(time.Second)) {
		t.Fatal("byte credit did not refill")
	}
}
func TestRateLimitAccountCacheIsBounded(t *testing.T) {
	limits := NewRateLimits()
	now := time.Unix(100, 0)
	for i := 0; i < api.UDPRateLimitMaxAccounts; i++ {
		if !limits.allowAt(strconv.Itoa(i), 0, false, now) {
			t.Fatal("cache full too early")
		}
	}
	if limits.allowAt("extra", 0, false, now) {
		t.Fatal("active account cache grew without bound")
	}
	if !limits.allowAt("extra", 0, false, now.Add(api.UDPRateLimitIdleTTL)) {
		t.Fatal("idle entries were not reclaimed")
	}
}
func TestAccountRateBudgetSharedUnderContention(t *testing.T) {
	limits := NewRateLimits()
	now := time.Unix(100, 0)
	var passed atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 64; j++ {
				if limits.allowAt("a", 0, false, now) {
					passed.Add(1)
				}
			}
		}()
	}
	wg.Wait()
	if got := passed.Load(); got != api.UDPPacketBurstPerAccount {
		t.Fatalf("concurrent burst allowed %d, want %d", got, api.UDPPacketBurstPerAccount)
	}
}
