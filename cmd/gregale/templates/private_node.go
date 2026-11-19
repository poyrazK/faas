package templates

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

const PrivateNode = "hello-node-private"

// MaterializePrivateNode names both the Compose project and its single app.
func MaterializePrivateNode(dest, slug string) error {
	if !api.ValidAppSlug(slug) || api.IsReservedAppSlug(slug) {
		return fmt.Errorf("invalid private Node app name %q", slug)
	}
	if err := Materialize(PrivateNode, dest); err != nil {
		return err
	}
	compose, err := FS.ReadFile(PrivateNode + "/compose.yaml")
	if err != nil {
		return err
	}
	data := strings.ReplaceAll(string(compose), "private-node", slug)
	return os.WriteFile(filepath.Join(dest, "compose.yaml"), []byte(data), 0o644)
}
