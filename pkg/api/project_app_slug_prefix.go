package api

import (
	"context"
	"mime/multipart"
)

type projectAppSlugPrefixContextKey struct{}

// ContextWithProjectAppSlugPrefix makes the next project scan send
// app_slug_prefix (`gregale deploy --project --prefix`): new apps are named
// <prefix><service>. The plan token keeps the resolved names for apply.
func ContextWithProjectAppSlugPrefix(ctx context.Context, prefix string) context.Context {
	if prefix == "" {
		return ctx
	}
	return context.WithValue(ctx, projectAppSlugPrefixContextKey{}, prefix)
}

func writeProjectAppSlugPrefix(ctx context.Context, w *multipart.Writer) error {
	prefix, _ := ctx.Value(projectAppSlugPrefixContextKey{}).(string)
	if prefix == "" {
		return nil
	}
	return w.WriteField("app_slug_prefix", prefix)
}
