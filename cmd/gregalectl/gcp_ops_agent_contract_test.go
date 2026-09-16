package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGCPOpsAgentArmoredKeyContract(t *testing.T) {
	t.Parallel()
	repoRoot := filepath.Join("..", "..")
	defaultsPath := filepath.Join(repoRoot, "deploy", "ansible", "roles", "gcp_ops_agent", "defaults", "main.yml")
	tasksPath := filepath.Join(repoRoot, "deploy", "ansible", "roles", "gcp_ops_agent", "tasks", "main.yml")

	defaults, err := os.ReadFile(defaultsPath)
	if err != nil {
		t.Fatal(err)
	}
	tasks, err := os.ReadFile(tasksPath)
	if err != nil {
		t.Fatal(err)
	}

	defaultsText := string(defaults)
	for _, token := range []string{
		"faas_gcp_ops_agent_key_url: https://packages.cloud.google.com/apt/doc/apt-key.gpg",
		"faas_gcp_ops_agent_key_fingerprint: 35BAA0B33E9EB396F59CA838C0BA5CE6DC6315A3",
		"faas_gcp_ops_agent_keyring: /usr/share/keyrings/google-cloud-ops-agent.asc",
	} {
		if !strings.Contains(defaultsText, token) {
			t.Errorf("%s is missing armored-key contract %q", defaultsPath, token)
		}
	}
	if strings.Contains(defaultsText, "faas_gcp_ops_agent_keyring: /usr/share/keyrings/google-cloud-ops-agent.gpg") {
		t.Errorf("%s stores Google's ASCII-armored key under a binary .gpg name", defaultsPath)
	}

	tasksText := string(tasks)
	for _, token := range []string{
		"--show-keys",
		"--with-colons",
		"faas_gcp_ops_agent_key_fingerprint in faas_gcp_ops_agent_key_info.stdout",
	} {
		if !strings.Contains(tasksText, token) {
			t.Errorf("%s is missing signing-identity verification %q", tasksPath, token)
		}
	}
}
