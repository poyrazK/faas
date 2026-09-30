-- +goose Up
-- ADR-375: route and edge configuration writers share publication's app lock.
CREATE TRIGGER clone_route_policy_publication_fence BEFORE INSERT OR DELETE OR UPDATE OF
app_id, account_id, project_id, environment_slug, only_allow_declared_routes, declared_routes ON project_environment_route_policies
FOR EACH ROW EXECUTE FUNCTION serialize_clone_value_publication();
CREATE TRIGGER clone_edge_policy_publication_fence BEFORE INSERT OR DELETE OR UPDATE OF
app_id, account_id, project_id, environment_slug, rules ON project_environment_edge_policies
FOR EACH ROW EXECUTE FUNCTION serialize_clone_value_publication();

-- +goose Down
DROP TRIGGER clone_edge_policy_publication_fence ON project_environment_edge_policies;
DROP TRIGGER clone_route_policy_publication_fence ON project_environment_route_policies;
