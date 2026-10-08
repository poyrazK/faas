// postgres-probe is a shell-free, provider-neutral disposable customer app.
package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/e2etest/postgresprobe"
)

func main() {
	major, err := strconv.Atoi(os.Getenv("POSTGRES_PROBE_MAJOR"))
	if err != nil {
		fmt.Fprintln(os.Stderr, postgresprobe.ErrProbe.Error()+": configuration_major")
		os.Exit(1)
	}
	config := postgresprobe.FromEnv(os.Getenv, major)
	mode, err := postgresprobe.Mode(os.Args)
	if err != nil {
		fmt.Fprintln(os.Stderr, postgresprobe.ErrProbe.Error()+": configuration_command")
		os.Exit(1)
	}
	if mode == "migrate" {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if err := config.Migrate(ctx); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println("PostgreSQL release migration verified")
		return
	}
	if mode == "check-runtime" {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if _, err := config.Check(ctx); err != nil {
			fmt.Fprintln(os.Stderr, postgresprobe.ErrProbe)
			os.Exit(1)
		}
		fmt.Println("PostgreSQL runtime task verified")
		return
	}
	if mode != "serve" {
		fmt.Fprintln(os.Stderr, postgresprobe.ErrProbe)
		os.Exit(1)
	}
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	server := http.Server{Addr: ":" + port, Handler: config.Handler(), ReadHeaderTimeout: 5 * time.Second}
	if err := server.ListenAndServe(); err != nil {
		fmt.Fprintln(os.Stderr, postgresprobe.ErrProbe)
		os.Exit(1)
	}
}
