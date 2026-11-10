// Package githubscope carries a verified deployment target through webhook and
// check projections. It narrows database lookups; it never grants authorization.
package githubscope

import (
	"context"
	"crypto/sha256"
	"fmt"
)

type accountKey struct{}

func WithAccount(ctx context.Context, accountID string) context.Context {
	return context.WithValue(ctx, accountKey{}, accountID)
}
func Account(ctx context.Context) string { id, _ := ctx.Value(accountKey{}).(string); return id }
func Tag(ctx context.Context) string {
	id := Account(ctx)
	if id == "" {
		return ""
	}
	digest := sha256.Sum256([]byte(id))
	return fmt.Sprintf("%x", digest[:8])
}
