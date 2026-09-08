// Package faas is the public Go SDK for the onebox FaaS platform.
//
// It exposes a single Client type that talks to apid over HTTPS and
// returns typed responses for every operation. The package follows
// the conventions of the cloudflare-go, AWS SDK v2, and fly-go SDKs:
// one root package, one Client struct, variadic functional options,
// typed *APIError with errors.As/errors.Is support.
//
// # Authentication
//
// Pass a bearer token to NewClient:
//
//	c, err := faas.NewClient("https://api.example.com", os.Getenv("FAAS_TOKEN"))
//	if err != nil {
//	    return err
//	}
//
// An empty token disables the Authorization header; the only operations
// that work without auth are the device-code flow (MintCliAuthCode,
// ExchangeCliAuthCode) and the public status page (GetStatusSLO).
//
// # Errors
//
// Every 4xx/5xx with a Problem-shaped body returns an *APIError. Use
// errors.As to extract the typed error and read its Problem field:
//
//	app, err := c.GetApp(ctx, "hello-world")
//	var apiErr *faas.APIError
//	if errors.As(err, &apiErr) {
//	    log.Printf("api: %s (%s)", apiErr.Code, apiErr.Detail)
//	}
//
// For common cases, use the sentinel errors with errors.Is:
//
//	if errors.Is(err, faas.ErrNotFound) { ... }
//	if errors.Is(err, faas.ErrRateLimited) { ... }
//	if errors.Is(err, faas.ErrCapacity) { ... }
//	if errors.Is(err, faas.ErrUnauthorized) { ... }
//
// # Idempotency
//
// Every mutating call (POST/PATCH/DELETE) carries an Idempotency-Key
// header. The SDK auto-mints a UUIDv4 if the caller does not supply
// one. The server's replay middleware (apid/server.go::idempotent)
// caches the response for 24h, so retrying with the same key returns
// the same response.
//
// For CI pipelines and explicit retry logic, pin a stable key:
//
//	ctx = faas.WithIdempotencyKey(ctx, "deploy-attempt-3")
//	dep, err := c.Deploy(ctx, slug, req)
//
// The same key on a retried call returns the cached response without
// re-running the deploy.
//
// # Pagination
//
// Cursor-walking helpers exist for collection endpoints. Today only
// deployments have a cursor; for everything else, the single-page
// method is sufficient (Free/Hobby plans don't paginate).
//
//	all, err := c.ListDeploymentsAll(ctx)
//	appDeployments, err := c.ListAppDeploymentsAll(ctx, "hello-world")
//
// # Streaming
//
// SSE streams (app logs, deployment logs, dashboard events) return
// an io.ReadCloser plus a Decoder for typed frames:
//
//	body, err := c.StreamAppLogs(ctx, slug, "", true)
//	if err != nil {
//	    return err
//	}
//	defer body.Close()
//	dec := faas.NewDecoder(body)
//	for ev := range dec.Events() {
//	    fmt.Println(ev.Data)
//	}
//	if err := <-dec.Errors(); err != nil {
//	    return err
//	}
//
// # Concurrency
//
// A Client is safe for concurrent use. The HTTP transport is shared;
// the only mutable state is the read-only token and base URL. SSE
// Decoders are NOT safe for shared use — each goroutine that reads
// a stream should construct its own Decoder.
//
// # Go version
//
// The SDK targets Go 1.23. No features newer than 1.23 are used.
package faas
