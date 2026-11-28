from typing import Literal

CreateKeyRequestApprovalCategoriesItem = Literal["billing", "credentials", "delete", "production"]

CREATE_KEY_REQUEST_APPROVAL_CATEGORIES_ITEM_VALUES: set[CreateKeyRequestApprovalCategoriesItem] = {
    "billing",
    "credentials",
    "delete",
    "production",
}


def check_create_key_request_approval_categories_item(value: str) -> CreateKeyRequestApprovalCategoriesItem:
    if value in CREATE_KEY_REQUEST_APPROVAL_CATEGORIES_ITEM_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {CREATE_KEY_REQUEST_APPROVAL_CATEGORIES_ITEM_VALUES!r}"
    )
