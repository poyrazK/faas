// adr: 126
package apidserver_test

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/wire"
)

func TestAPIDListenerFallsBackWithoutActivation(t *testing.T) {
	_ = os.Unsetenv("LISTEN_PID")
	_ = os.Unsetenv("LISTEN_FDS")
	listener, err := apidserver.ApidListener("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("apidListener: %v", err)
	}
	defer listener.Close()
	if !strings.HasPrefix(listener.Addr().String(), "127.0.0.1:") {
		t.Fatalf("listener = %s, want loopback", listener.Addr())
	}
}

func TestActivatedAPIDListenerQueuesAcrossRestart(t *testing.T) {
	base, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	tcpBase := base.(*net.TCPListener)
	master, err := tcpBase.File()
	_ = base.Close()
	if err != nil {
		t.Fatal(err)
	}
	defer master.Close()
	addr := tcpBase.Addr().String()

	first, err := apidserver.ActivatedAPIDListener(master, addr)
	if err != nil {
		t.Fatal(err)
	}
	_ = first.Close()

	client, err := net.DialTimeout("tcp", addr, time.Second)
	if err != nil {
		t.Fatalf("connection during restart was refused: %v", err)
	}
	defer client.Close()
	_ = client.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := fmt.Fprintln(client, "queued"); err != nil {
		t.Fatal(err)
	}

	replacement, err := apidserver.ActivatedAPIDListener(master, addr)
	if err != nil {
		t.Fatal(err)
	}
	defer replacement.Close()
	served := make(chan error, 1)
	go func() {
		conn, acceptErr := replacement.Accept()
		if acceptErr != nil {
			served <- acceptErr
			return
		}
		defer conn.Close()
		line, readErr := bufio.NewReader(conn).ReadString('\n')
		if readErr == nil && line != "queued\n" {
			readErr = fmt.Errorf("request = %q", line)
		}
		if readErr == nil {
			_, readErr = fmt.Fprintln(conn, "ready")
		}
		served <- readErr
	}()
	line, err := bufio.NewReader(client).ReadString('\n')
	if err != nil || line != "ready\n" {
		t.Fatalf("queued connection response = %q, %v", line, err)
	}
	if err := <-served; err != nil {
		t.Fatal(err)
	}
}

func TestActivatedAPIDListenerAcceptsNarrowerBindThanWildcardConfig(t *testing.T) {
	base, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	tcpBase := base.(*net.TCPListener)
	master, err := tcpBase.File()
	_ = base.Close()
	if err != nil {
		t.Fatal(err)
	}
	defer master.Close()

	port := tcpBase.Addr().(*net.TCPAddr).Port
	listener, err := apidserver.ActivatedAPIDListener(master, fmt.Sprintf("0.0.0.0:%d", port))
	if err != nil {
		t.Fatalf("narrower activated bind rejected: %v", err)
	}
	_ = listener.Close()

	if _, err := apidserver.ActivatedAPIDListener(master, fmt.Sprintf("0.0.0.0:%d", port+1)); err == nil {
		t.Fatal("activated listener accepted a different configured port")
	}
}

func TestAPIDListenerRejectsIncompleteActivation(t *testing.T) {
	t.Setenv("LISTEN_PID", fmt.Sprint(os.Getpid()))
	_ = os.Unsetenv("LISTEN_FDS")
	_, err := apidserver.ApidListener("tcp", "127.0.0.1:0")
	if err == nil || !strings.Contains(err.Error(), "incomplete") {
		t.Fatalf("error = %v, want incomplete activation", err)
	}
}

func TestAPIDListenerRejectsMultipleDescriptors(t *testing.T) {
	t.Setenv("LISTEN_PID", fmt.Sprint(os.Getpid()))
	t.Setenv("LISTEN_FDS", "2")
	_, err := apidserver.ApidListener("tcp", "127.0.0.1:0")
	if err == nil || !strings.Contains(err.Error(), "exactly 1") {
		t.Fatalf("error = %v, want descriptor-count rejection", err)
	}
}

// TestAPIDListenerLeavesWireUsableUnderActivation reproduces rc.278's apid
// start failure: under faas-apid.socket, apidListener consumed fd 3 and then
// called wire.Share while LISTEN_PID/LISTEN_FDS were still set, so pkg/wire
// loaded its activation set from a closed fd and the metrics listener failed
// with "inspect inherited fd 3: bad file descriptor". The child process gets
// a real listening socket as fd 3, exactly as systemd passes it.
func TestAPIDListenerLeavesWireUsableUnderActivation(t *testing.T) {
	if os.Getenv("APID_ACTIVATION_CHILD") == "1" {
		_ = os.Setenv("LISTEN_PID", strconv.Itoa(os.Getpid()))
		_ = os.Setenv("LISTEN_FDS", "1")
		_ = os.Setenv("LISTEN_FDNAMES", "api")
		api, err := apidserver.ApidListener("tcp", os.Getenv("APID_ACTIVATION_ADDR"))
		if err != nil {
			fmt.Println("api listener:", err)
			os.Exit(2)
		}
		defer api.Close()
		metrics, err := wire.ListenInherited("tcp", "127.0.0.1:0")
		if err != nil {
			fmt.Println("metrics listener:", err)
			os.Exit(3)
		}
		_ = metrics.Close()
		os.Exit(0)
	}

	base, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	socket, err := base.(*net.TCPListener).File()
	addr := base.Addr().String()
	_ = base.Close()
	if err != nil {
		t.Fatal(err)
	}
	defer socket.Close()

	cmd := exec.Command(os.Args[0], "-test.run=^TestAPIDListenerLeavesWireUsableUnderActivation$")
	cmd.Env = append(os.Environ(), "APID_ACTIVATION_CHILD=1", "APID_ACTIVATION_ADDR="+addr)
	cmd.ExtraFiles = []*os.File{socket} // fd 3 in the child
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("activated apid child: %v\n%s", err, out)
	}
}
