package fcvm

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/profileproto"
)

// fakeCaptureGuest reads one framed request and answers with reply.
func fakeCaptureGuest(t *testing.T, conn net.Conn, reply []byte) {
	t.Helper()
	defer func() { _ = conn.Close() }()
	var hdr [8]byte
	if _, err := io.ReadFull(conn, hdr[:]); err != nil {
		t.Error(err)
		return
	}
	if binary.BigEndian.Uint32(hdr[:4]) != profileproto.CaptureMessageType {
		t.Error("wrong message type")
	}
	body := make([]byte, binary.BigEndian.Uint32(hdr[4:]))
	if _, err := io.ReadFull(conn, body); err != nil {
		t.Error(err)
		return
	}
	var req profileproto.CaptureRequest
	if err := json.Unmarshal(body, &req); err != nil || req.Validate() != nil {
		t.Errorf("guest received invalid request: %v %+v", err, req)
	}
	_, _ = conn.Write(reply)
}

func framedCaptureReply(t *testing.T, result profileproto.CaptureResult) []byte {
	t.Helper()
	body, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]byte, 5, 5+len(body))
	binary.BigEndian.PutUint32(out[1:], uint32(len(body)))
	return append(out, body...)
}

func TestExchangeProfileCapture(t *testing.T) {
	req, _ := json.Marshal(profileproto.CaptureRequest{CaptureID: strings.Repeat("0a", 16), Kinds: []string{"heap"}, DurationMillis: 1000})
	ok := profileproto.CaptureResult{Profiles: []profileproto.CapturedProfile{{Kind: "heap", ProcessID: "7", Profile: []byte("pprof")}}, Processes: 1}
	tooMany := profileproto.CaptureResult{Profiles: make([]profileproto.CapturedProfile, profileproto.CaptureMaxProfiles+1)}
	for i := range tooMany.Profiles {
		tooMany.Profiles[i] = profileproto.CapturedProfile{Kind: "cpu", Profile: []byte("x")}
	}
	tests := []struct {
		name    string
		reply   []byte
		wantErr error
		wantAny bool
	}{
		{name: "ok", reply: framedCaptureReply(t, ok)},
		{name: "busy", reply: []byte{profileproto.CaptureAckBusy}, wantErr: ErrProfileCaptureBusy},
		{name: "suspended", reply: []byte{profileproto.CaptureAckSuspended}, wantErr: ErrProfileCaptureSuspended},
		{name: "old guest", reply: []byte{7}, wantErr: ErrProfileCaptureUnsupported},
		{name: "oversized length", reply: []byte{0, 0xff, 0xff, 0xff, 0xff}, wantAny: true},
		{name: "unbounded result", reply: framedCaptureReply(t, tooMany), wantAny: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			host, guest := net.Pipe()
			defer func() { _ = host.Close() }()
			go fakeCaptureGuest(t, guest, tt.reply)
			got, err := exchangeProfileCapture(host, req)
			switch {
			case tt.wantErr != nil:
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("err = %v, want %v", err, tt.wantErr)
				}
			case tt.wantAny:
				if err == nil {
					t.Fatal("unbounded reply accepted")
				}
			default:
				if err != nil || len(got.Profiles) != 1 || string(got.Profiles[0].Profile) != "pprof" {
					t.Fatalf("got %+v, %v", got, err)
				}
			}
		})
	}
}

func TestManagerCaptureProfileRequiresLiveAppInstance(t *testing.T) {
	m := &Manager{vmm: &JailerVMM{}, live: map[string]*Instance{"job": {IsJob: true}}}
	req := profileproto.CaptureRequest{CaptureID: strings.Repeat("0a", 16), Kinds: []string{"cpu"}, DurationMillis: 1000}
	for _, id := range []string{"missing", "job"} {
		if _, err := m.CaptureProfile(t.Context(), id, req); !errors.Is(err, ErrProfileCaptureNotRunning) {
			t.Fatalf("%s: err = %v", id, err)
		}
	}
}
