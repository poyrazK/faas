package api

// PreviewClient carries the methods of preview and internal capabilities
// (ADR-20261023115741). Client keeps the generally available and beta
// ones, so the public faas.Client exposes only those; faas/preview.Client
// adds these. The Client sits one embedding deeper than in faas.Client, so
// a type embedding both resolves every Client method through faas.Client.
type PreviewClient struct{ previewClient }

type previewClient struct{ *Client }

// NewPreviewClient returns the preview methods for c; they share its
// transport, base URL and token.
func NewPreviewClient(c *Client) *PreviewClient {
	return &PreviewClient{previewClient{c}}
}
