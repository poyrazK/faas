package main_test

import (
	"bytes"
	"github.com/onebox-faas/faas/cmd/gregale"
	"strings"
	"testing"
)

func TestProductControlIsHiddenFromCustomerDiscovery(t *testing.T) {
	for _, command := range main.CustomerCliCommands() {
		if command.Name == "product" {
			t.Fatal("Product Control is listed in customer commands")
		}
	}
	for _, tc := range []struct {
		name   string
		render func() int
	}{
		{"bash", main.CmdCompletionBash},
		{"zsh", main.CmdCompletionZsh},
		{"fish", main.CmdCompletionFish},
		{"powershell", main.CmdCompletionPowershell},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			main.CaptureStdoutSwap(t, &out, tc.render)
			for _, private := range []string{"Product Control", "product-control", "\"product\"", "'product'", "_gregale_product"} {
				if strings.Contains(out.String(), private) {
					t.Errorf("%s completion exposes %q", tc.name, private)
				}
			}
		})
	}
}
