from typing import Literal

OrgAgentPolicyRequestAllowedScopesItem = Literal["deploy:write", "postgres:manage", "secrets:request", "storage:manage"]

ORG_AGENT_POLICY_REQUEST_ALLOWED_SCOPES_ITEM_VALUES: set[OrgAgentPolicyRequestAllowedScopesItem] = {
    "deploy:write",
    "postgres:manage",
    "secrets:request",
    "storage:manage",
}


def check_org_agent_policy_request_allowed_scopes_item(value: str) -> OrgAgentPolicyRequestAllowedScopesItem:
    if value in ORG_AGENT_POLICY_REQUEST_ALLOWED_SCOPES_ITEM_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {ORG_AGENT_POLICY_REQUEST_ALLOWED_SCOPES_ITEM_VALUES!r}"
    )
