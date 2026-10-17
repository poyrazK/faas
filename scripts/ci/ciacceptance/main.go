// ciacceptance inspects native acceptance evidence or retries journal-owned
// intent/storage cleanup. It has no VM control or new-execution admission path.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/onebox-faas/faas/tests/ci/acceptance"
)

func main() {
	action := flag.String("action", "verdict", "verdict, cleanup or finalize")
	dir := flag.String("evidence", "", "private native evidence directory")
	flag.Parse()
	if err := run(*action, *dir); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(action, dir string) error {
	switch action {
	case "verdict":
		j, err := acceptance.ReadJournal(dir)
		if err != nil {
			return err
		}
		if err := acceptance.Verdict(j); err != nil {
			return err
		}
		fmt.Println("Gregale CI native acceptance: passed")
		return nil
	case "cleanup":
		if os.Getenv(acceptance.OptInEnv) != "1" {
			return fmt.Errorf("CI acceptance cleanup requires explicit native-host opt-in")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
		defer cancel()
		return acceptance.Cleanup(ctx, os.Getenv("GREGALE_CI_ACCEPTANCE_DATABASE_URL"), os.Getenv("GREGALE_CI_ACCEPTANCE_GCS_BUCKET"), dir)
	case "finalize":
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		return acceptance.Finalize(ctx, os.Getenv("GREGALE_CI_ACCEPTANCE_DATABASE_URL"), dir)
	default:
		return fmt.Errorf("unknown CI acceptance action")
	}
}
