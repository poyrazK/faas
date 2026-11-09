from typing import Literal

CreateOrgAPIKeyRequestApprovalCategoriesItem = Literal["billing", "credentials", "delete", "production"]

CREATE_ORG_API_KEY_REQUEST_APPROVAL_CATEGORIES_ITEM_VALUES: set[CreateOrgAPIKeyRequestApprovalCategoriesItem] = {
    "billing",
    "credentials",
    "delete",
    "production",
}


def check_create_org_api_key_request_approval_categories_item(
    value: str,
) -> CreateOrgAPIKeyRequestApprovalCategoriesItem:
    if value in CREATE_ORG_API_KEY_REQUEST_APPROVAL_CATEGORIES_ITEM_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {CREATE_ORG_API_KEY_REQUEST_APPROVAL_CATEGORIES_ITEM_VALUES!r}"
    )
