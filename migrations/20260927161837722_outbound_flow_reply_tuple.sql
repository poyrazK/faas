-- filename: 20260927161837722_outbound_flow_reply_tuple.sql

-- +goose Up
-- +goose StatementBegin
-- Keep the host conntrack reply destination beside the original tuple. It
-- may reflect host-side SNAT, but upstream provider NAT remains unobserved.
ALTER TABLE outbound_flow_events
    ADD COLUMN reply_destination_ip inet,
    ADD COLUMN reply_destination_port integer CHECK (reply_destination_port BETWEEN 0 AND 65535),
    ADD CONSTRAINT outbound_flow_events_reply_pair_check
        CHECK ((reply_destination_ip IS NULL) = (reply_destination_port IS NULL));
CREATE INDEX outbound_flow_events_reply_time_idx ON outbound_flow_events
    (reply_destination_ip, reply_destination_port, observed_at DESC)
    WHERE reply_destination_ip IS NOT NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX outbound_flow_events_reply_time_idx;
ALTER TABLE outbound_flow_events
    DROP CONSTRAINT outbound_flow_events_reply_pair_check,
    DROP COLUMN reply_destination_port,
    DROP COLUMN reply_destination_ip;
-- +goose StatementEnd
