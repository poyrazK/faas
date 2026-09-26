from typing import Literal

CreateOutboundIntegrationRequestAllowedMethodsItem = Literal["DELETE", "GET", "HEAD", "PATCH", "POST", "PUT"]

CREATE_OUTBOUND_INTEGRATION_REQUEST_ALLOWED_METHODS_ITEM_VALUES: set[
    CreateOutboundIntegrationRequestAllowedMethodsItem
] = {
    "DELETE",
    "GET",
    "HEAD",
    "PATCH",
    "POST",
    "PUT",
}


def check_create_outbound_integration_request_allowed_methods_item(
    value: str,
) -> CreateOutboundIntegrationRequestAllowedMethodsItem:
    if value in CREATE_OUTBOUND_INTEGRATION_REQUEST_ALLOWED_METHODS_ITEM_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {CREATE_OUTBOUND_INTEGRATION_REQUEST_ALLOWED_METHODS_ITEM_VALUES!r}"
    )
