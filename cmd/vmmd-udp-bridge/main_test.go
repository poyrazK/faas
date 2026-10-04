package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/udpwire"
)

// Execute the actual helper entrypoint in a child test binary, preserving
// stdout exclusively for framed datagrams rather than testing a substitute.
func TestUDPHelperProcess(t *testing.T) {
	if os.Getenv("GREGALE_UDP_HELPER_TEST_PROCESS") != "1" {
		return
	}
	os.Args = append([]string{"vmmd-udp-bridge"}, os.Args[3:]...)
	main()
	os.Exit(0)
}

func udpHelperCommand(t *testing.T, args ...string) *exec.Cmd {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	t.Cleanup(cancel)
	cmd := exec.CommandContext(ctx, os.Args[0], append([]string{"-test.run=^TestUDPHelperProcess$", "--"}, args...)...)
	cmd.Env = append(os.Environ(), "GREGALE_UDP_HELPER_TEST_PROCESS=1", "GORACE=atexit_sleep_ms=0")
	return cmd
}

func TestUDPHelperReadinessDatagramsAndEOF(t *testing.T) {
	guest, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer guest.Close()
	cmd := udpHelperCommand(t, "127.0.0.1", strconv.Itoa(guest.LocalAddr().(*net.UDPAddr).Port))
	ready, readyChild, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer ready.Close()
	defer readyChild.Close()
	cmd.ExtraFiles = []*os.File{readyChild}
	input, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	waited := false
	defer func() {
		if !waited {
			_ = input.Close()
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	}()
	_ = readyChild.Close()
	line, err := bufio.NewReader(ready).ReadString('\n')
	if err != nil || line != "OK\n" {
		t.Fatalf("readiness=%q err=%v", line, err)
	}
	for _, want := range [][]byte{nil, {0, 255, 0, 1}, bytes.Repeat([]byte{42}, 4096)} {
		if err := udpwire.Write(input, want); err != nil {
			t.Fatal(err)
		}
		if err := guest.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
			t.Fatal(err)
		}
		buffer := make([]byte, 8192)
		n, peer, err := guest.ReadFromUDP(buffer)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(buffer[:n], want) {
			t.Fatalf("guest datagram=%v want=%v", buffer[:n], want)
		}
		if _, _, err := guest.WriteMsgUDP(buffer[:n], nil, peer); err != nil {
			t.Fatal(err)
		}
		got, err := udpwire.Read(output)
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("framed reply=%v err=%v", got, err)
		}
	}
	if err := input.Close(); err != nil {
		t.Fatal(err)
	}
	err = cmd.Wait()
	waited = true
	if err != nil || stderr.Len() != 0 {
		t.Fatalf("helper EOF exit=%v stderr=%s", err, &stderr)
	}
}

func TestUDPHelperRejectsInvalidArguments(t *testing.T) {
	for _, args := range [][]string{{}, {"127.0.0.1"}, {"::1", "9000"}, {"127.0.0.1", "0"}, {"127.0.0.1", "65536"}} {
		t.Run(strconv.Itoa(len(args))+":"+strings.Join(args, ":"), func(t *testing.T) {
			cmd := udpHelperCommand(t, args...)
			var output, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &output, &stderr
			err := cmd.Run()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 2 || output.Len() != 0 || stderr.Len() == 0 {
				t.Fatalf("invalid helper args exit=%v stdout=%s stderr=%s", err, &output, &stderr)
			}
		})
	}
}

func TestUDPHelperRejectsMissingOrNonPipeReadiness(t *testing.T) {
	guest, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer guest.Close()
	for _, kind := range []string{"missing", "regular-file", "read-only-pipe"} {
		t.Run(kind, func(t *testing.T) {
			cmd := udpHelperCommand(t, "127.0.0.1", strconv.Itoa(guest.LocalAddr().(*net.UDPAddr).Port))
			var file *os.File
			if kind == "regular-file" {
				var err error
				file, err = os.CreateTemp(t.TempDir(), "unchanged")
				if err != nil {
					t.Fatal(err)
				}
				defer file.Close()
				cmd.ExtraFiles = []*os.File{file}
			} else if kind == "read-only-pipe" {
				reader, writer, err := os.Pipe()
				if err != nil {
					t.Fatal(err)
				}
				defer reader.Close()
				defer writer.Close()
				cmd.ExtraFiles = []*os.File{reader}
			}
			var output, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &output, &stderr
			err := cmd.Run()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 4 || output.Len() != 0 {
				t.Fatalf("readiness rejection: exit=%v stdout=%s stderr=%s", err, &output, &stderr)
			}
			if file != nil {
				info, err := file.Stat()
				if err != nil || info.Size() != 0 {
					t.Fatalf("invalid readiness file modified: stat=%v err=%v", info, err)
				}
			}
		})
	}
	if err := guest.SetReadDeadline(time.Now().Add(20 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	var buffer [32]byte
	if n, _, err := guest.ReadFromUDP(buffer[:]); err == nil {
		t.Fatalf("invalid readiness leaked %d bytes to guest", n)
	}
}
