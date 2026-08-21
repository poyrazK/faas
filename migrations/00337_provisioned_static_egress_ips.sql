-- filename: 00337_provisioned_static_egress_ips.sql
-- +goose Up
-- +goose StatementBegin

-- 00337_provisioned_static_egress_ips.sql — ADR-119 redesign
-- (resolution of PR-997 reviewer's BLOCKING issue #3 — BYOIP is
-- impossible in many v1 deployment shapes:
-- https://github.com/onebox-faas/faas/pull/997 review).
--
-- The customer-supplied IP must be **operator-provisioned** (an
-- additional IP attached to the host's AS), not freely supplied
-- by the customer. An IP that isn't routed to the host's AS is
-- (a) outbound-spoofed at the switch — no internet path, and
-- (b) inbound replies route to the customer AS — packets are
-- lost. The fix is to gate the apid handler on operator
-- provisioning: the customer can only pin an IP that the
-- operator has declared as routed on this host.
--
-- This table is the gate. vmmd writes it on SIGHUP from the
-- operator TOML /etc/faas/egress/static_egress_ips.toml. apid
-- reads it at PUT /v1/apps/{slug}/static-egress-ip time. A
-- row means "the operator has provisioned this IP on the host's
-- AS and aliased it on br-tenants"; absence means "the
-- customer's IP would not reach the public internet — refuse
-- the pin".
--
-- Why a separate table (not a column on apps): the gate is
-- operator-state, not per-app-state. The bundle is loaded once
-- per host and shared across every app that wants to pin a
-- static IP. Adding/removing an entry is an operator action
-- (edit TOML, SIGHUP), not a customer action.
--
-- Schema:
--   account_id, customer_ip is the composite PK so a customer
--   can have multiple provisioning rows (one per IP) and an IP
--   can be provisioned on multiple accounts (cross-account
--   sharing is the operator's call — the v1 schema does not
--   enforce single-account-per-IP).
--   family=4 CHECK pins v4 only (v6 deferred; same as the
--   apps.static_egress_ip family check in migration 00336).
--   customer_ip index supports the apid lookup "is this IP in
--   any operator's bundle?" — the handler also uses the
--   (account_id, customer_ip) PK directly for the strict
--   account-scoped check.

CREATE TABLE IF NOT EXISTS provisioned_static_egress_ips (
    account_id  UUID        NOT NULL,
    customer_ip INET        NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (account_id, customer_ip),
    CONSTRAINT provisioned_static_egress_ips_family_check
        CHECK (family(customer_ip) = 4)
);

CREATE INDEX IF NOT EXISTS provisioned_static_egress_ips_customer_ip_idx
    ON provisioned_static_egress_ips (customer_ip);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS provisioned_static_egress_ips_customer_ip_idx;
DROP TABLE IF EXISTS provisioned_static_egress_ips;

-- +goose StatementEnd
