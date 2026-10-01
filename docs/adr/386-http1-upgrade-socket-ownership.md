# ADR-386: HTTP/1 upgrade socket ownership in the gateway

Date: 2026-10-01
Status: Accepted

## Context

ADR-080 carries Upgrade traffic through vmmd's authenticated raw RPC and a
guest-network bridge. The helper's response-head framing is corrected by
ADR-384's companion networking fix. The gateway still treated a successful
101 as an ordinary HTTP response: it read the original request body, closed
the RPC send side, and wrote protocol frames through `ResponseWriter`.
Response-recorder tests accepted this impossible HTTP body shape. Real TCP
connections stalled before the handshake, even with a warm guest and a correct
installed bridge response.

## Decision

After a valid raw-RPC 101 response, gatewayd-internal writes the response headers
and takes ownership of the HTTP/1 connection through `ResponseController.Hijack`.
The buffered socket reader supplies client frames, including bytes read ahead
with the request head. Guest frames write directly to that connection. A writer
without the hijacking capability cannot serve this transport.

The HTTP request body can still reach the guest before its response. The RPC send
side remains open until the response selects either an upgraded socket reader or
an ordinary non-101 rejection. Client EOF cancels the receive side as well as
closing the send side. Upstream EOF cancels a blocked client reader. A successful
101 detaches the ordinary request budget; activity, idle bounds, the existing
24-hour session ceiling, and vmmd byte limits remain authoritative.

Admission, authentication, guest metadata filtering, node lookup, and vmmd
ownership do not change. The gateway records response status and bytes even
though upgraded bytes bypass ordinary HTTP response-body framing. Send failures
are communicated to the receiver through a channel rather than racing on the
session outcome label.

## Consequences and validation

Real TCP tests must cover handshake delivery, exact binary bytes in both
directions, request-head read-ahead, wrapper/recorder accounting, and bounded
disconnect cleanup. Non-101 refusals remain ordinary HTTP responses. Public
acceptance additionally exercises actual WebSocket frames through the public
reverse proxy and Cloudflare, including ping/pong, fragmentation, and close.

This changes gateway transport ownership. Guest init, Firecracker, the VM state
machine, and snapshot lifecycle are unchanged. Native OCI lifecycle qualification
remains a separate release requirement under ADR-387.
