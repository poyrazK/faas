from typing import Literal

APIKeyResponseApprovalCategoriesItem = Literal["billing", "credentials", "delete", "production"]

API_KEY_RESPONSE_APPROVAL_CATEGORIES_ITEM_VALUES: set[APIKeyResponseApprovalCategoriesItem] = {
    "billing",
    "credentials",
    "delete",
    "production",
}


def check_api_key_response_approval_categories_item(value: str) -> APIKeyResponseApprovalCategoriesItem:
    if value in API_KEY_RESPONSE_APPROVAL_CATEGORIES_ITEM_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {API_KEY_RESPONSE_APPROVAL_CATEGORIES_ITEM_VALUES!r}")
