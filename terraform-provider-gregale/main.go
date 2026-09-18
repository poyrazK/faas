package main

import (
	"context"
	"log"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/poyrazK/faas/terraform-provider-gregale/internal/provider"
)

var version = "dev"

func main() {
	ctx := context.Background()
	err := providerserver.Serve(ctx, provider.New(version), providerserver.ServeOpts{
		Address: "registry.terraform.io/gregale/gregale",
	})
	if err != nil {
		log.Fatal(err)
	}
}
