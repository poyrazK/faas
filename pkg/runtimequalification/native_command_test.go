package runtimequalification

// adr: 687

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"
)

func TestNativeCommandFixture(t *testing.T) {
	switch os.Getenv("GREGALE_NATIVE_HELPER_CASE") {
	case "environment":
		fmt.Print(os.Getenv("GREGALE_NATIVE_CANARY"))
		os.Exit(0)
	case "exit":
		os.Exit(7)
	case "wait":
		for {
			time.Sleep(time.Second)
		}
	}
}

func TestNativeCommandDoesNotInheritOperatorEnvironment(t *testing.T) {
	t.Setenv("GREGALE_NATIVE_CANARY", "operator-value-must-not-reach-child")
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	result, err := nativeCommand(t.Context(), binary, []string{"-test.run=^TestNativeCommandFixture$"}, "", []string{"GREGALE_NATIVE_HELPER_CASE=environment"})
	if err != nil || result.ExitCode != 0 || len(result.Stdout) != 0 {
		t.Fatal("inherited operator environment", result.ExitCode, err, string(result.Stdout))
	}
	result, err = nativeCommand(t.Context(), binary, []string{"-test.run=^TestNativeCommandFixture$"}, "", []string{"GREGALE_NATIVE_HELPER_CASE=exit"})
	if err != nil || result.ExitCode != 7 {
		t.Fatal("lost real process exit", result.ExitCode, err)
	}
}

func TestNativeCommandCancellationAndOutputBudget(t *testing.T) {
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	_, err = nativeCommand(ctx, binary, []string{"-test.run=^TestNativeCommandFixture$"}, "", []string{"GREGALE_NATIVE_HELPER_CASE=wait"})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("lost command cancellation", err)
	}
	canceled := false
	output := &cappedOutput{limit: 4, cancel: func() { canceled = true }}
	n, err := output.Write([]byte("oversized"))
	if n != 4 || !errors.Is(err, ErrEvidence) || !canceled || !bytes.Equal(output.Bytes(), []byte("over")) {
		t.Fatal("unbounded command output", n, err)
	}
}
