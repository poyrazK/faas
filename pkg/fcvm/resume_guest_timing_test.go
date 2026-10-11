// adr: 022 — the resume ack's optional guest timing trailer is additive.
package fcvm

import (
	"net"
	"testing"
)

func TestReadResumeGuestTiming(t *testing.T) {
	cases := []struct {
		name   string
		guest  []byte
		want   resumeGuestTiming
		wantOK bool
	}{
		{name: "current guest", guest: []byte{'T', 1, 0, 3, 0, 12, 1, 44, 0, 0},
			want: resumeGuestTiming{ReadMs: 3, KernelMs: 12, ReseedMs: 300}, wantOK: true},
		{name: "older guest closes without a trailer", guest: nil},
		{name: "unknown trailer version is ignored", guest: []byte{'T', 9, 0, 3, 0, 12, 1, 44, 0, 0}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			host, guest := net.Pipe()
			defer func() { _ = host.Close() }()
			go func() {
				_, _ = guest.Write(tc.guest)
				_ = guest.Close()
			}()
			got, ok := readResumeGuestTiming(host)
			if ok != tc.wantOK || got != tc.want {
				t.Fatalf("readResumeGuestTiming = %+v, %v; want %+v, %v", got, ok, tc.want, tc.wantOK)
			}
		})
	}
}
