package e2etest

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/workloadidentity"
)

// The agent run daemons (ADR-20261027090010, ADR-20261027090014). agentd claims agent runs and
// drives them through schedd; vmmd relays a run VM's model calls to
// outboundd, which authorizes them against the run and calls the provider;
// githubd forwards pull request deliveries to apid's bridge as
// gregale.github events. Each runs on the harness's own sockets and ports.

// AgentDaemons is how a test reaches the agent run daemons.
type AgentDaemons struct {
	// BridgeSock is apid's githubd bridge socket; GithubdSock is githubd's
	// own gRPC socket.
	BridgeSock  string
	GithubdSock string
	// RelaySock is outboundd's relay listener, which vmmd dials.
	RelaySock string
	// GithubdURL is githubd's webhook origin; GitHubWebhookSecret signs
	// deliveries posted to <GithubdURL>/webhooks/github.
	GithubdURL          string
	GitHubWebhookSecret string
	// FleetRecipient and FleetIdentity seal and open customer credentials:
	// apid seals with the recipient, outboundd opens with the identity.
	FleetRecipient string
	FleetIdentity  string
	// IdentityKey and IdentityJWKS sign and verify the relay identities
	// vmmd mints for agent runs.
	IdentityKey  string
	IdentityJWKS string
}

const agentIdentityKeyID = "e2e-agent-relay"

// prepareAgentDaemons fixes the agent daemons' keys and sockets before apid
// and vmmd boot, and returns the environment every daemon then shares.
func prepareAgentDaemons(t *testing.T, h *Harness, which Which) []string {
	t.Helper()
	if which&(Agentd|Outboundd|Githubd) == 0 {
		return nil
	}
	d := &h.AgentDaemons
	var env []string
	if which&Githubd != 0 {
		d.BridgeSock = filepath.Join(h.SockDir, "apid-githubd.sock")
		secret := make([]byte, 32)
		if _, err := rand.Read(secret); err != nil {
			t.Fatalf("e2etest: webhook secret: %v", err)
		}
		d.GitHubWebhookSecret = hex.EncodeToString(secret)
		d.GithubdSock = filepath.Join(h.SockDir, "githubd.sock")
		// apid and agentd reach this githubd, never the host's production one.
		env = append(env, "FAAS_APID_GITHUBD_BRIDGE_SOCK="+d.BridgeSock, "FAAS_GITHUBD_SOCKET="+d.GithubdSock)
	}
	if which&Outboundd != 0 {
		keys := filepath.Join(h.TmpDir, "agent-keys")
		if err := os.MkdirAll(keys, 0o700); err != nil {
			t.Fatalf("e2etest: mkdir agent keys: %v", err)
		}
		d.FleetIdentity, d.FleetRecipient = filepath.Join(keys, "fleet.age"), filepath.Join(keys, "fleet.age.pub")
		identity, err := secretbox.GenerateAndSaveHostKey(d.FleetIdentity)
		if err != nil {
			t.Fatalf("e2etest: fleet identity: %v", err)
		}
		if err := secretbox.WriteRecipientFile(d.FleetRecipient, identity); err != nil {
			t.Fatalf("e2etest: fleet recipient: %v", err)
		}
		d.IdentityKey, d.IdentityJWKS = writeAgentIdentityKey(t, keys)
		d.RelaySock = filepath.Join(h.SockDir, "outbound-relay.sock")
		env = append(env,
			"FAAS_FLEET_AGE_RECIPIENT_PATH="+d.FleetRecipient,
			"FAAS_WORKLOAD_IDENTITY_KEY_PATH="+d.IdentityKey,
			"FAAS_WORKLOAD_IDENTITY_KEY_ID="+agentIdentityKeyID,
			"FAAS_VMMD_OUTBOUND_RELAY_TARGET=unix://"+d.RelaySock,
		)
	}
	return env
}

// writeAgentIdentityKey writes vmmd's relay signing key and the JWKS
// outboundd verifies its tokens with.
func writeAgentIdentityKey(t *testing.T, dir string) (string, string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("e2etest: relay identity key: %v", err)
	}
	signer, err := workloadidentity.NewSigner(key, workloadidentity.DefaultIssuer, agentIdentityKeyID, workloadidentity.DefaultTokenTTL)
	if err != nil {
		t.Fatalf("e2etest: relay identity signer: %v", err)
	}
	jwks, err := json.Marshal(signer.JWKS())
	if err != nil {
		t.Fatalf("e2etest: relay identity JWKS: %v", err)
	}
	keyPath, jwksPath := filepath.Join(dir, "workload-identity.pem"), filepath.Join(dir, "workload-identity.jwks.json")
	block := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	if err := os.WriteFile(keyPath, block, 0o600); err != nil {
		t.Fatalf("e2etest: write relay identity key: %v", err)
	}
	if err := os.WriteFile(jwksPath, jwks, 0o644); err != nil {
		t.Fatalf("e2etest: write relay identity JWKS: %v", err)
	}
	return keyPath, jwksPath
}

// startAgentDaemons boots the selected agent daemons after the core set:
// outboundd before any run can call it, githubd once apid's bridge listens,
// and agentd once schedd does.
func startAgentDaemons(t *testing.T, h *Harness, bin, dbURL string, which Which, extraEnv []string) {
	t.Helper()
	if which&Outboundd != 0 {
		startOutboundd(t, h, bin, dbURL, extraEnv)
	}
	if which&Githubd != 0 {
		startGithubd(t, h, bin, dbURL, extraEnv)
	}
	if which&Agentd != 0 {
		ensureOptionalBinary(t, bin, "agentd")
		metrics := freeTCPAddr(t)
		env := append(testEnvCommon(dbURL),
			"FAAS_AGENTD_AGENT_RUNS=1",
			"FAAS_AGENTD_SCHEDD_TARGET=unix://"+h.ScheddSock,
			"FAAS_AGENTD_METRICS_ADDR="+metrics,
			"FAAS_AGENTD_LEASE_TTL=15s",
		)
		env = append(env, extraEnv...)
		h.procs = append(h.procs, startProc(t, bin, "agentd", env))
		waitTCP(t, metrics, 30*time.Second)
	}
}

func startOutboundd(t *testing.T, h *Harness, bin, dbURL string, extraEnv []string) {
	t.Helper()
	ensureOptionalBinary(t, bin, "outboundd")
	d := h.AgentDaemons
	listen, metrics := freeTCPAddr(t), freeTCPAddr(t)
	cfg := fmt.Sprintf("agent_runs_enabled = true\nlisten_addr = %q\nmetrics_addr = %q\nrelay_listen = %q\nworkload_identity_jwks_path = %q\nai_gateway_enabled = true\n",
		listen, metrics, "unix://"+d.RelaySock, d.IdentityJWKS)
	cfgPath := filepath.Join(h.TmpDir, "outboundd.toml")
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatalf("e2etest: write outboundd.toml: %v", err)
	}
	env := append(testEnvCommon(dbURL),
		"FAAS_E2E_OUTBOUNDD_CONFIG="+cfgPath,
		"FAAS_FLEET_AGE_IDENTITY_PATH="+d.FleetIdentity,
	)
	env = append(env, extraEnv...)
	h.procs = append(h.procs, startProc(t, bin, "outboundd", env))
	waitUnix(t, d.RelaySock, 30*time.Second)
}

func startGithubd(t *testing.T, h *Harness, bin, dbURL string, extraEnv []string) {
	t.Helper()
	ensureOptionalBinary(t, bin, "githubd")
	d := &h.AgentDaemons
	waitUnix(t, d.BridgeSock, 30*time.Second)
	// githubd opens sealed installation tokens with the host identity and
	// refuses to boot without one.
	hostKey := filepath.Join(h.TmpDir, "githubd-host.age")
	if _, err := secretbox.GenerateAndSaveHostKey(hostKey); err != nil {
		t.Fatalf("e2etest: githubd host identity: %v", err)
	}
	httpAddr := freeTCPAddr(t)
	d.GithubdURL = "http://" + httpAddr
	cfg := fmt.Sprintf("http_addr = %q\nsocket_path = %q\n", httpAddr, d.GithubdSock)
	cfgPath := filepath.Join(h.TmpDir, "githubd.toml")
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatalf("e2etest: write githubd.toml: %v", err)
	}
	env := append(testEnvCommon(dbURL),
		"FAAS_E2E_GITHUBD_CONFIG="+cfgPath,
		"FAAS_GITHUB_WEBHOOK_SECRET="+d.GitHubWebhookSecret,
		"FAAS_GITHUBD_AUTOMATION_EVENTS=1",
		"FAAS_GITHUBD_WORK_DIR="+filepath.Join(h.TmpDir, "githubd-work"),
		"FAAS_HOST_AGE_IDENTITY_PATH="+hostKey,
	)
	env = append(env, extraEnv...)
	h.procs = append(h.procs, startProc(t, bin, "githubd", env))
	waitTCP(t, httpAddr, 30*time.Second)
}

// ensureOptionalBinary builds a daemon the shared set leaves out, unless a
// prebuilt copy is already in bin (FAAS_E2E_BIN_DIR).
func ensureOptionalBinary(t *testing.T, bin, name string) {
	t.Helper()
	if binaryPresent(filepath.Join(bin, name)) {
		return
	}
	mod, err := moduleImportPath()
	if err != nil {
		t.Fatalf("e2etest: module path: %v", err)
	}
	var out bytes.Buffer
	cmd := exec.Command("go", "build", "-o", filepath.Join(bin, name), mod+"/cmd/"+name)
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("e2etest: go build %s: %v\n%s", name, err, out.String())
	}
}
