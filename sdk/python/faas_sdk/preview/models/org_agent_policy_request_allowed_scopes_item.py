from typing import Literal

OrgAgentPolicyRequestAllowedScopesItem = Literal[
    "data:read", "deploy:write", "env:write", "postgres:manage", "secrets:request", "storage:manage", "usage:read"
]

ORG_AGENT_POLICY_REQUEST_ALLOWED_SCOPES_ITEM_VALUES: set[OrgAgentPolicyRequestAllowedScopesItem] = {
    "data:read",
    "deploy:write",
    "env:write",
    "postgres:manage",
    "secrets:request",
    "storage:manage",
    "usage:read",
}


def check_org_agent_policy_request_allowed_scopes_item(value: str) -> OrgAgentPolicyRequestAllowedScopesItem:
    if value in ORG_AGENT_POLICY_REQUEST_ALLOWED_SCOPES_ITEM_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {ORG_AGENT_POLICY_REQUEST_ALLOWED_SCOPES_ITEM_VALUES!r}"
    )
