-- +goose Up
-- meterd renews this lease only while both canary progression and rollout
-- recovery ticks are succeeding. A stale or absent row blocks new canaries.
CREATE TABLE safe_release_worker_lease (
    singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
    healthy_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    CONSTRAINT safe_release_worker_lease_expiry CHECK (expires_at > healthy_at)
);

-- +goose Down
DROP TABLE safe_release_worker_lease;
