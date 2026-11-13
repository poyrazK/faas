package gateway

// TakeEdgePlacement exposes gatewayd-internal's handling of the edge's
// placement request to the external forward-path tests (ADR-20261031105055).
var TakeEdgePlacement = takeEdgePlacement

// RecordEdgeSecretHeader exposes the sealed request header hand-off to the
// external forward-path tests (ADR-20261110210000 slice 3i).
var RecordEdgeSecretHeader = recordEdgeSecretHeader
