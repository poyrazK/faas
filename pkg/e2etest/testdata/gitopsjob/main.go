// gitopsjob is the scratch Dockerfile customer's executable in native GitOps tests.
package main

import (
	"fmt"
	"os"
	"strings"
	"time"
)

func main() {
	data, err := os.ReadFile("/app/commit.txt")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	commit := strings.TrimSpace(string(data))
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "--build-hold":
			fmt.Println("gitops-build-hold:" + commit)
			time.Sleep(2 * time.Minute)
			return
		case "--smoke":
			fmt.Println("gitops-smoke:" + commit)
			return
		}
	}
	run := os.Getenv("GREGALE_RUN_ID")
	if run == "" {
		fmt.Fprintln(os.Stderr, "missing scheduled run identity")
		os.Exit(1)
	}
	fmt.Println("gitops-run:" + commit + ":" + run)
}
