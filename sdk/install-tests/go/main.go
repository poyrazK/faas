// Run from an independent module against the public mirror tree.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	faas "github.com/gregale-dev/gregale-go"
)

func main() {
	client, err := faas.NewClient(os.Args[1], "sdk-install-fixture")
	if err != nil {
		panic(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	account, err := client.Whoami(ctx)
	if err != nil {
		panic(err)
	}
	if account.Plan != "hobby" {
		panic("wrong account plan")
	}
	app, err := client.GetApp(ctx, "hello-world")
	if err != nil {
		panic(err)
	}
	if app.Slug != "hello-world" {
		panic("wrong app slug")
	}
	_, err = client.GetApp(ctx, "missing-app-404")
	if !errors.Is(err, faas.ErrNotFound) {
		panic(fmt.Sprintf("expected ErrNotFound, got %v", err))
	}
	fmt.Println("Go installed SDK: account, typed app and typed 404 verified")
}
