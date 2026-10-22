-- filename: 20261013103609192_environment_serving_gateway_roster.sql

-- A route is ready only when every acknowledgement belongs to the current
-- gateway roster. Equal counts alone can accept a stale gateway after a roster
-- replacement with the same cardinality.
-- +goose Up
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION environment_workload_serving_gateway_roster_ready(target_graph uuid) RETURNS boolean
LANGUAGE sql STABLE AS $$
SELECT EXISTS (
    SELECT 1
    FROM environment_workload_serving_receipts receipt
    WHERE receipt.graph_id=target_graph
        AND cardinality(receipt.expected_gateways)=(
            SELECT count(DISTINCT gateway.name)
            FROM unnest(receipt.expected_gateways) AS gateway(name)
        )
        AND NOT EXISTS (
            SELECT 1 FROM unnest(receipt.expected_gateways) AS gateway(name)
            WHERE gateway.name IS NULL OR btrim(gateway.name)=''
        )
        AND (
            (NOT EXISTS (
                SELECT 1 FROM environment_workload_serving_routes route
                WHERE route.graph_id=target_graph
            ) AND cardinality(receipt.expected_gateways)=0)
            OR (EXISTS (
                SELECT 1 FROM environment_workload_serving_routes route
                WHERE route.graph_id=target_graph
            ) AND cardinality(receipt.expected_gateways)=(
                SELECT count(*) FROM compute_nodes node
                WHERE node.active AND node.role IN ('compute-only','compute-node')
                    AND nullif(btrim(node.gateway_target_url),'') IS NOT NULL
            ) AND NOT EXISTS (
                SELECT 1 FROM compute_nodes node
                WHERE node.active AND node.role IN ('compute-only','compute-node')
                    AND nullif(btrim(node.gateway_target_url),'') IS NOT NULL
                    AND NOT (node.name=ANY(receipt.expected_gateways))
            ))
        )
        AND NOT EXISTS (
            SELECT 1
            FROM environment_workload_serving_routes route
            CROSS JOIN LATERAL unnest(receipt.expected_gateways) AS gateway(name)
            WHERE route.graph_id=target_graph AND NOT EXISTS (
                SELECT 1 FROM environment_workload_serving_route_acks ack
                WHERE ack.graph_id=route.graph_id AND ack.route_generation=route.route_generation
                    AND ack.node_name=gateway.name
            )
        )
        AND NOT EXISTS (
            SELECT 1
            FROM environment_workload_serving_route_acks ack
            WHERE ack.graph_id=target_graph
                AND NOT (ack.node_name=ANY(receipt.expected_gateways))
        )
)
$$;
-- +goose StatementEnd

-- Keep the scheduled-Job proof from the preceding migration, and add the
-- exact gateway-roster predicate to the final readiness function used by the
-- serving receipt guard and the completion transaction.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION environment_workload_serving_ready(target_graph uuid) RETURNS boolean
LANGUAGE sql STABLE AS $$
SELECT environment_workload_serving_ready_without_scheduled_jobs(target_graph)
    AND environment_workload_serving_gateway_roster_ready(target_graph)
    AND EXISTS (
        SELECT 1 FROM environment_workload_serving_receipts receipt
        JOIN project_release_sets release ON release.id=receipt.release_set_id AND release.active
        WHERE receipt.graph_id=target_graph
            AND environment_workload_scheduled_jobs_ready(target_graph,release.id)
    )
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION environment_workload_serving_ready(target_graph uuid) RETURNS boolean
LANGUAGE sql STABLE AS $$
SELECT environment_workload_serving_ready_without_scheduled_jobs(target_graph)
    AND EXISTS (
        SELECT 1 FROM environment_workload_serving_receipts receipt
        JOIN project_release_sets release ON release.id=receipt.release_set_id AND release.active
        WHERE receipt.graph_id=target_graph
            AND environment_workload_scheduled_jobs_ready(target_graph,release.id)
    )
$$;
-- +goose StatementEnd
DROP FUNCTION environment_workload_serving_gateway_roster_ready(uuid);
