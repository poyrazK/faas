package api

// OutboundRelayURL is GREGALE_OUTBOUND_URL inside every app instance and
// Run: guest-init's loopback relay to outboundd (ADR-989). guest-init keeps
// its own copy, which a guest-init test holds equal to this one; `gregale
// add ai` writes app settings from it (ADR 20261027132936).
const OutboundRelayURL = "http://127.0.0.1:2774"
