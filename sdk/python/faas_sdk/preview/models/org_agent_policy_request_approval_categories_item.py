from typing import Literal

OrgAgentPolicyRequestApprovalCategoriesItem = Literal["billing", "credentials", "delete", "production"]

ORG_AGENT_POLICY_REQUEST_APPROVAL_CATEGORIES_ITEM_VALUES: set[OrgAgentPolicyRequestApprovalCategoriesItem] = {
    "billing",
    "credentials",
    "delete",
    "production",
}


def check_org_agent_policy_request_approval_categories_item(value: str) -> OrgAgentPolicyRequestApprovalCategoriesItem:
    if value in ORG_AGENT_POLICY_REQUEST_APPROVAL_CATEGORIES_ITEM_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {ORG_AGENT_POLICY_REQUEST_APPROVAL_CATEGORIES_ITEM_VALUES!r}"
    )
