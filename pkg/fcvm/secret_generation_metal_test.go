//go:build metal

// adr:438
package fcvm

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
)

// This receiver exercises guest VSOCK and metadata forwarding. vmmd/store
// tests exercise the production authorization and transactional generation fence.
type metalSecretGenerationLedger struct {
	mu          sync.Mutex
	generations map[string]string
	active      map[string]bool
	starts      map[string][]string
	acks        map[string]string
}

const metalSecretRevision = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func installMetalSecretGenerationReceiver(t *testing.T, m *Manager) *metalSecretGenerationLedger {
	t.Helper()
	ledger := &metalSecretGenerationLedger{generations: map[string]string{}, active: map[string]bool{}, starts: map[string][]string{}, acks: map[string]string{}}
	vmm, ok := m.vmm.(*JailerVMM)
	if !ok {
		t.Fatal("fixture VMM is not a JailerVMM")
	}
	if err := vmm.RegisterGuestVsockStreamHandler(VsockRuntimeConfigHostPort, ledger.handle); err != nil {
		t.Fatal(err)
	}
	return ledger
}
func (l *metalSecretGenerationLedger) handle(instance string, conn net.Conn) (string, error) {
	var length [4]byte
	if _, err := io.ReadFull(conn, length[:]); err != nil {
		return "read", err
	}
	size := binary.BigEndian.Uint32(length[:])
	if size > 64*1024 {
		return "read", fmt.Errorf("oversized fixture request")
	}
	body := make([]byte, size)
	if _, err := io.ReadFull(conn, body); err != nil {
		return "read", err
	}
	var req struct {
		Kind       string `json:"kind"`
		Workload   string `json:"workload_name"`
		Generation string `json:"generation"`
		Previous   string `json:"previous_generation"`
		Revision   string `json:"revision"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return "read", err
	}
	response := l.respond(instance, req.Kind, req.Workload, req.Generation, req.Previous, req.Revision)
	body, err := json.Marshal(response)
	if err != nil {
		return "marshal", err
	}
	binary.BigEndian.PutUint32(length[:], uint32(len(body)))
	if _, err = conn.Write(length[:]); err != nil {
		return "write", err
	}
	_, err = conn.Write(body)
	return "", err
}
func (l *metalSecretGenerationLedger) respond(instance, kind, workload, generation, previous, revision string) map[string]any {
	l.mu.Lock()
	defer l.mu.Unlock()
	key := instance + ":" + workload
	current := l.generations[key]
	switch kind {
	case "secrets":
		secrets := map[string]string{}
		if workload == "reload" {
			secrets = map[string]string{"DATABASE_URL": "reload-DATABASE_URL", "TOKEN": "reload-TOKEN"}
		}
		return map[string]any{"revision": metalSecretRevision, "secrets": secrets}
	case "secret_generation_start":
		if len(generation) != 32 || current == generation && !l.active[key] || current != "" && current != generation && current != previous {
			return map[string]any{"error": "secret_generation_stale"}
		}
		if current != generation {
			l.starts[key] = append(l.starts[key], generation)
			delete(l.acks, key)
		}
		l.generations[key], l.active[key] = generation, true
		return map[string]any{"accepted": true, "generation": generation}
	case "secret_generation_retire":
		if generation != current {
			return map[string]any{"error": "secret_generation_stale"}
		}
		l.active[key] = false
		delete(l.acks, key)
		return map[string]any{"accepted": true, "generation": generation}
	case "secret_reload_ack":
		if generation != current || !l.active[key] || revision != metalSecretRevision {
			return map[string]any{"error": "secret_generation_stale"}
		}
		l.acks[key] = generation
		return map[string]any{"accepted": true, "revision": revision}
	case "secret_reload_status":
		return map[string]any{"accepted": true, "revision": revision}
	default:
		return map[string]any{"error": "invalid_request"}
	}
}
func (l *metalSecretGenerationLedger) assertAck(t *testing.T, instance, workload string, restarted bool) {
	t.Helper()
	l.mu.Lock()
	defer l.mu.Unlock()
	key := instance + ":" + workload
	if l.acks[key] == "" || l.acks[key] != l.generations[key] || !l.active[key] {
		t.Fatal("guest ACK did not echo its registered execution identity")
	}
	starts := l.starts[key]
	if restarted && (len(starts) < 2 || starts[0] == starts[1]) {
		t.Fatal("same-version restart did not register a fresh execution identity")
	}
}

func metalSecretAckCGI(busybox, generationFile string) string {
	return "#!/bin/sh\nprintf 'Content-Type: text/plain\\r\\n\\r\\n'\n" +
		fmt.Sprintf("generation=$(%s cat %s)\nrevision=$(%s cat /tmp/reload-revision)\nendpoint=$(%s cat /tmp/ack-endpoint)\n", busybox, generationFile, busybox, busybox) +
		fmt.Sprintf("%s wget -q -O /dev/null --post-data \"{\\\"revision\\\":\\\"$revision\\\",\\\"status\\\":\\\"applied\\\",\\\"generation\\\":\\\"$generation\\\"}\" \"$endpoint\" 2>/dev/null\nprintf 'exit=%%s' \"$?\"\n", busybox)
}
func metalSecretProjectionWait(busybox string) string {
	return fmt.Sprintf("while [ -z \"$(%s cat \"$FAAS_SECRETS_REVISION_FILE\")\" ]; do %s sleep 0.1; done\n", busybox, busybox) +
		fmt.Sprintf("%s cat \"$FAAS_SECRETS_REVISION_FILE\" > /tmp/reload-revision\nprintf '%%s' \"$FAAS_SECRETS_RELOAD_GENERATION\" > /tmp/current-generation\nprintf '%%s' \"$FAAS_SECRETS_RELOAD_ACK_ENDPOINT\" > /tmp/ack-endpoint\n", busybox)
}

func assertMetalSecretAckResult(t *testing.T, body []byte, err error, accepted bool) {
	t.Helper()
	want := "exit=1"
	if accepted {
		want = "exit=0"
	}
	if err != nil || strings.TrimSpace(string(body)) != want {
		t.Fatalf("guest generation ACK result=%q err=%v want=%s", body, err, want)
	}
}
