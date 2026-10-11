package main

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
)

const (
	testDeliverySinkFixture = "delivery-sink"
	testTCPRelayFixture     = "tcp-relay"
	testTCPEchoFixture      = "tcp-echo"
)

func knownScenarioFixture(name string) bool {
	return name == testDeliverySinkFixture || name == testTCPRelayFixture || name == testTCPEchoFixture
}

//go:embed scenario_fixtures/delivery-sink/package.json scenario_fixtures/delivery-sink/server.js scenario_fixtures/tcp-relay/package.json scenario_fixtures/tcp-relay/server.js scenario_fixtures/tcp-echo/package.json scenario_fixtures/tcp-echo/server.js
var scenarioFixtureFS embed.FS

func materializeScenarioFixture(name string) (string, func(), error) {
	if !knownScenarioFixture(name) {
		return "", nil, fmt.Errorf("unknown scenario fixture %q", name)
	}
	source, err := fs.Sub(scenarioFixtureFS, "scenario_fixtures/"+name)
	if err != nil {
		return "", nil, err
	}
	dir, err := os.MkdirTemp("", "gregale-test-fixture-*")
	if err != nil {
		return "", nil, err
	}
	if err := os.CopyFS(dir, source); err != nil {
		_ = os.RemoveAll(dir)
		return "", nil, err
	}
	return dir, func() { _ = os.RemoveAll(dir) }, nil
}
