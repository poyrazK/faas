package main_test

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

// Prod hunt r34: `queue send`, `dlq list` and others say --app "defaults to
// linked context" but printed their usage in a linked checkout. The linked
// app fills only a missing leading <slug>.
func TestWithLinkedAppFillsOnlyTheMissingSlug(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".git", "HEAD"), []byte("ref: refs/heads/main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "gregale.yaml"), []byte("project:\n  name: shop\n  app: shop-web\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(repo)
	const row = "3bb3e87a-a171-4fc6-8e93-0b20f47e0bf5"
	for _, tc := range []struct {
		in   []string
		n    int
		want []string
	}{
		{nil, 1, []string{"shop-web"}},
		{[]string{row}, 2, []string{"shop-web", row}},
		// Review of r35: a lone slug (`queue ack billing`, or `--app billing`
		// merged in) is not a row id, so the link does not take its place.
		{[]string{"billing"}, 2, []string{"billing"}},
		{[]string{"billing"}, 1, []string{"billing"}},
		{[]string{"billing", row}, 2, []string{"billing", row}},
	} {
		if got := main.WithLinkedApp(tc.in, tc.n); !slices.Equal(got, tc.want) {
			t.Errorf("withLinkedApp(%q, %d) = %q, want %q", tc.in, tc.n, got, tc.want)
		}
	}
	t.Chdir(t.TempDir())
	if got := main.WithLinkedApp(nil, 1); len(got) != 0 {
		t.Errorf("no link: got %q, want nothing", got)
	}
}
