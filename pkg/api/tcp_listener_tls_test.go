package api

import (
	"strings"
	"testing"
)

func TestTCPListenerTLSConfig(t *testing.T) {
	for _, test := range []struct {
		name   string
		config TCPListenerTLSConfig
		valid  bool
	}{
		{"legacy", TCPListenerTLSConfig{}, true},
		{"passthrough", TCPListenerTLSConfig{Mode: TCPListenerTLSPassthrough}, true},
		{"terminate", TCPListenerTLSConfig{Mode: TCPListenerTLSTerminate, Hostname: " Echo.Example "}, true},
		{"punycode", TCPListenerTLSConfig{Mode: TCPListenerTLSTerminate, Hostname: "xn--bcher-kva.example"}, true},
		{"numeric-labels", TCPListenerTLSConfig{Mode: TCPListenerTLSTerminate, Hostname: "1.2.3.example"}, true},
		{"numeric-domain", TCPListenerTLSConfig{Mode: TCPListenerTLSTerminate, Hostname: "1.2"}, true},
		{"unknown", TCPListenerTLSConfig{Mode: "invalid"}, false},
		{"unused-host", TCPListenerTLSConfig{Hostname: "echo.example"}, false},
		{"missing-host", TCPListenerTLSConfig{Mode: TCPListenerTLSTerminate}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			result, err := test.config.Normalize()
			if (err == nil) != test.valid {
				t.Fatalf("result=%+v err=%v", result, err)
			}
			if test.name == "terminate" && result.Hostname != "echo.example" {
				t.Fatalf("hostname=%q", result.Hostname)
			}
		})
	}
	for _, hostname := range []string{"127.0.0.1", "::1", "*.example", "localhost", "echo.example.", "echo..example", "-echo.example", "echo-.example", "echo_example.com", "bücher.example", "echo.example/path", strings.Repeat("a", TCPListenerTLSDNSLabelMaxBytes+1) + ".example", strings.Repeat("a.", 128) + "example"} {
		if _, err := (TCPListenerTLSConfig{Mode: TCPListenerTLSTerminate, Hostname: hostname}).Normalize(); err == nil {
			t.Errorf("accepted invalid hostname %q", hostname)
		}
	}
	for _, hostname := range []string{"1.2.3.999", "001.002.003.004", "999.999.999.999"} {
		if _, err := (TCPListenerTLSConfig{Mode: TCPListenerTLSTerminate, Hostname: hostname}).Normalize(); err == nil {
			t.Errorf("accepted IPv4-shaped hostname %q", hostname)
		}
	}
}
