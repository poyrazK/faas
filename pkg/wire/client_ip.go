package wire

// ClientIPHeader is the single-value, gateway-derived client IP header sent
// to customer workloads. It is populated only after the gateway validates the
// trusted public-to-internal X-Forwarded-For hop; customer-supplied values are
// discarded before forwarding.
const ClientIPHeader = "x-faas-client-ip"
