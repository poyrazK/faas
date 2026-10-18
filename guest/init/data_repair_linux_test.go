//go:build linux

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/google/uuid"
	core "github.com/onebox-faas/faas/pkg/datarepair"
	repairguest "github.com/onebox-faas/faas/pkg/datarepair/guest"
)

func TestDataRepairGuestModeIsExclusive(t *testing.T) {
	marker := []byte(`{"kind":"data_repair","version":1}`)
	for _, mode := range []string{"valid", "invalid", "unknown", appTaskManifestRelativePath, executionManifestRelativePath, "etc/faas/job.json", "etc/faas/build.json"} {
		t.Run(mode, func(t *testing.T) {
			fsys := fstest.MapFS{dataRepairManifestRelativePath: &fstest.MapFile{Data: marker}}
			switch mode {
			case "valid":
			case "invalid":
				fsys[dataRepairManifestRelativePath].Data = []byte(`{"kind":"execution","version":1}`)
			case "unknown":
				fsys[dataRepairManifestRelativePath].Data = []byte(`{"kind":"data_repair","version":1,"extra":true}`)
			default:
				fsys[mode] = &fstest.MapFile{Data: []byte(`{}`)}
			}
			got, _, err := decideMode(fsys)
			if got != modeDataRepair || (err != nil) != (mode != "valid") {
				t.Fatalf("mode=%v err=%v", got, err)
			}
		})
	}
}

type repairPipeListener struct{ conn net.Conn }

func (l repairPipeListener) Accept() (net.Conn, error) { return l.conn, nil }
func (l repairPipeListener) Close() error              { return l.conn.Close() }
func (l repairPipeListener) Addr() net.Addr            { return &net.IPAddr{IP: net.IPv4(127, 0, 0, 1)} }

func TestDataRepairGuestOneShotAndCancellation(t *testing.T) {
	for _, cancelInput := range []bool{false, true} {
		t.Run(map[bool]string{false: "result_delivery", true: "cancel_input"}[cancelInput], func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			host, guest := net.Pipe()
			defer host.Close()
			called := make(chan struct{}, 1)
			done := make(chan error, 1)
			go func() {
				done <- serveDataRepairOnce(ctx, repairPipeListener{guest}, func(context.Context, core.RehearsalInput) repairguest.Result {
					called <- struct{}{}
					return repairguest.Result{FailureCode: "candidate_apply_and_checks"}
				})
			}()
			if cancelInput {
				_, _ = host.Write([]byte{0})
				cancel()
				select {
				case err := <-done:
					if err == nil {
						t.Fatal("cancelled input succeeded")
					}
				case <-time.After(2 * time.Second):
					t.Fatal("input cancellation leaked reader")
				}
				if len(called) != 0 {
					t.Fatal("incomplete request ran guest")
				}
				return
			}
			dump := []byte("snapshot")
			hash := sha256.Sum256(dump)
			checks := sha256.Sum256([]byte("SELECT true"))
			in := core.RehearsalInput{Plan: core.Plan{ID: uuid.NewString(), Digest: strings.Repeat("a", 64), ChecksSHA256: hex.EncodeToString(checks[:]), Candidates: 1, Scope: core.Scope{AccountID: uuid.NewString(), AppID: uuid.NewString(), Environment: "production"}}, Database: "postgres", CheckSQL: "SELECT true", Dump: dump, SnapshotSHA256: hex.EncodeToString(hash[:])}
			if err := core.WriteRehearsalInput(host, in); err != nil {
				t.Fatal(err)
			}
			var result repairguest.Result
			if err := json.NewDecoder(host).Decode(&result); err != nil || result.FailureCode != "candidate_apply_and_checks" {
				t.Fatal("terminal result", err)
			}
			_ = host.Close()
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			if len(called) != 1 {
				t.Fatal("guest request was replayed")
			}
		})
	}
}
