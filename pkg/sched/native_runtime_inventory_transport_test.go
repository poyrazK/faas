package sched

// adr: 777
import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"testing"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestNativeInventoryStreamRequiresCompleteContiguousBoundedFrames(t *testing.T) {
	raw := []byte("inventory")
	digest := sha256.Sum256(raw)
	data := &vmmdpb.NativeRuntimeInventoryFrame{Payload: raw}
	end := &vmmdpb.NativeRuntimeInventoryFrame{Offset: uint32(len(raw)), Complete: true, Sha256: digest[:]}
	cases := []struct {
		name     string
		frames   []*vmmdpb.NativeRuntimeInventoryFrame
		terminal error
		valid    bool
	}{
		{"complete", []*vmmdpb.NativeRuntimeInventoryFrame{data, end}, io.EOF, true},
		{"wrapped-eof", []*vmmdpb.NativeRuntimeInventoryFrame{data, end}, fmt.Errorf("stream: %w", io.EOF), true},
		{"missing-completion", []*vmmdpb.NativeRuntimeInventoryFrame{data}, io.EOF, false},
		{"lost-final-status", []*vmmdpb.NativeRuntimeInventoryFrame{data, end}, errors.New("lost reply"), false},
		{"extra-data", []*vmmdpb.NativeRuntimeInventoryFrame{data, end, data}, io.EOF, false},
		{"duplicate-completion", []*vmmdpb.NativeRuntimeInventoryFrame{data, end, end}, io.EOF, false},
		{"nil", []*vmmdpb.NativeRuntimeInventoryFrame{nil}, io.EOF, false},
		{"gap", []*vmmdpb.NativeRuntimeInventoryFrame{{Offset: 1, Payload: raw}}, io.EOF, false},
		{"empty", []*vmmdpb.NativeRuntimeInventoryFrame{{}}, io.EOF, false},
		{"digest-on-data", []*vmmdpb.NativeRuntimeInventoryFrame{{Payload: raw, Sha256: digest[:]}}, io.EOF, false},
		{"wrong-digest", []*vmmdpb.NativeRuntimeInventoryFrame{data, {Offset: uint32(len(raw)), Complete: true, Sha256: make([]byte, 32)}}, io.EOF, false},
		{"terminal-payload", []*vmmdpb.NativeRuntimeInventoryFrame{data, {Offset: uint32(len(raw)), Complete: true, Payload: raw, Sha256: digest[:]}}, io.EOF, false},
		{"oversize-chunk", []*vmmdpb.NativeRuntimeInventoryFrame{{Payload: make([]byte, api.NativeRuntimeInventoryChunkMaxBytes+1)}}, io.EOF, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			i := 0
			got, err := receiveNativeRuntimeInventory(func() (*vmmdpb.NativeRuntimeInventoryFrame, error) {
				if i == len(c.frames) {
					return nil, c.terminal
				}
				f := c.frames[i]
				i++
				return f, nil
			})
			if (err == nil) != c.valid || (!c.valid && got != nil) {
				t.Fatal("partial or malformed inventory consumed", err)
			}
		})
	}
	unknown := &vmmdpb.NativeRuntimeInventoryFrame{Payload: raw}
	unknown.ProtoReflect().SetUnknown([]byte{0x28, 0x01})
	if got, err := receiveNativeRuntimeInventory(func() (*vmmdpb.NativeRuntimeInventoryFrame, error) { return unknown, nil }); err == nil || got != nil {
		t.Fatal("unknown frame accepted")
	}
}

func TestNativeInventoryStreamRefusesTotalSizeOverflow(t *testing.T) {
	chunk := make([]byte, api.NativeRuntimeInventoryChunkMaxBytes)
	offset := 0
	got, err := receiveNativeRuntimeInventory(func() (*vmmdpb.NativeRuntimeInventoryFrame, error) {
		f := &vmmdpb.NativeRuntimeInventoryFrame{Offset: uint32(offset), Payload: chunk}
		offset += len(chunk)
		return f, nil
	})
	if err == nil || got != nil || offset != api.NativeRuntimeInventoryMaxBytes+len(chunk) {
		t.Fatal("oversize observation accepted or truncated", err, offset)
	}
}
