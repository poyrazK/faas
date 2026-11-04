-- The apps domains' certificates (ADR-20261103163954 §3). edgednsd is the
-- only writer.

-- name: InsertEdgeTLSCertificate :one
INSERT INTO edge_tls_certificates
    (names, ca, account_uri, serial, sha256, chain_pem, key_sealed, key_kid, not_before, not_after)
VALUES
    (@names::text[], @ca, @account_uri, @serial, @sha256, @chain_pem, @key_sealed, @key_kid, @not_before, @not_after)
RETURNING id;

-- name: NotifyEdgeTLSCertificate :exec
-- Sent inside the insert's transaction, so a listener hears of a row only
-- once it is committed. The payload is the row id.
SELECT pg_notify('edge_tls_certificate', @payload::text);

-- name: NewestEdgeTLSCertificateTerms :one
-- What edgednsd needs to decide on a renewal, without the chain or the key.
SELECT id, account_uri, key_kid, sha256, not_after
FROM edge_tls_certificates
WHERE names = @names::text[] AND ca = @ca
ORDER BY not_after DESC, id DESC
LIMIT 1;

-- name: DeleteEdgeTLSCertificatesExpiredBefore :execrows
DELETE FROM edge_tls_certificates WHERE not_after < @before;

-- name: LiveEdgeTLSCertificates :many
-- What a compute edge serves: for each set of names, the newest certificate
-- from ca that is valid now, with its chain and sealed key.
SELECT DISTINCT ON (names) id, names, chain_pem, key_sealed, not_before, not_after
FROM edge_tls_certificates
WHERE ca = @ca AND not_before <= now() AND not_after > now()
ORDER BY names, not_after DESC, id DESC;

-- name: EdgeTLSCertificateChains :many
-- Every stored chain, expired ones included: the Certificate Transparency
-- watch tells edgednsd's certificates from anyone else's by their keys.
SELECT chain_pem FROM edge_tls_certificates ORDER BY id;
