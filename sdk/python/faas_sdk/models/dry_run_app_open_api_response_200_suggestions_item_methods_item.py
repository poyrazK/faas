from typing import Literal

DryRunAppOpenAPIResponse200SuggestionsItemMethodsItem = Literal["delete", "get", "patch", "post", "put"]

DRY_RUN_APP_OPEN_API_RESPONSE_200_SUGGESTIONS_ITEM_METHODS_ITEM_VALUES: set[
    DryRunAppOpenAPIResponse200SuggestionsItemMethodsItem
] = {
    "delete",
    "get",
    "patch",
    "post",
    "put",
}


def check_dry_run_app_open_api_response_200_suggestions_item_methods_item(
    value: str,
) -> DryRunAppOpenAPIResponse200SuggestionsItemMethodsItem:
    if value in DRY_RUN_APP_OPEN_API_RESPONSE_200_SUGGESTIONS_ITEM_METHODS_ITEM_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {DRY_RUN_APP_OPEN_API_RESPONSE_200_SUGGESTIONS_ITEM_METHODS_ITEM_VALUES!r}"
    )
