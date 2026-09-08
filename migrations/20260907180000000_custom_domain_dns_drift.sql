-- F3 / issue #1397: a verified custom domain must be revoked when the
-- periodic doctor observes that its CNAME no longer points at Gregale.
-- +goose Up

ALTER TABLE custom_domains DROP CONSTRAINT IF EXISTS custom_domains_cert_status_chk;
ALTER TABLE custom_domains ADD CONSTRAINT custom_domains_cert_status_chk
    CHECK (cert_status IN ('pending', 'issued', 'renewing', 'failed', 'dns_drifted'));

-- +goose Down

UPDATE custom_domains SET cert_status = 'pending' WHERE cert_status = 'dns_drifted';
ALTER TABLE custom_domains DROP CONSTRAINT IF EXISTS custom_domains_cert_status_chk;
ALTER TABLE custom_domains ADD CONSTRAINT custom_domains_cert_status_chk
    CHECK (cert_status IN ('pending', 'issued', 'renewing', 'failed'));
