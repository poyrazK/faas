package functionmigration

// adr: 1547

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"time"
)

// Probe runs only the read-only SSH witness actions. Configuration is private
// test material; owner identities travel through stdin, never shell text.
func Probe(ctx context.Context, driver, config, node, action string, owner *Owner) error {
	if action != "owner" && action != "retired" && action != "leakcheck" {
		return errors.New("unsupported native host probe")
	}
	input, err := json.Marshal(owner)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "python3", driver, "--config", config, "--probe", node, action)
	cmd.Stdin = bytes.NewReader(input)
	output, err := cmd.Output()
	if err != nil {
		return errors.New("native host probe failed: " + action)
	}
	var reply map[string]any
	if json.Unmarshal(output, &reply) != nil {
		return errors.New("native host probe omitted its structured result")
	}
	key := action
	if action == "owner" {
		key = "verified"
	}
	if reply[key] != true {
		return errors.New("native host probe did not confirm: " + action)
	}
	return nil
}
