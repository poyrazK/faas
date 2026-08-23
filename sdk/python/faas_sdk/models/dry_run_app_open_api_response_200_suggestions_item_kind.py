from typing import Literal

DryRunAppOpenAPIResponse200SuggestionsItemKind = Literal["validate"]

DRY_RUN_APP_OPEN_API_RESPONSE_200_SUGGESTIONS_ITEM_KIND_VALUES: set[DryRunAppOpenAPIResponse200SuggestionsItemKind] = {
    "validate",
}


def check_dry_run_app_open_api_response_200_suggestions_item_kind(
    value: str,
) -> DryRunAppOpenAPIResponse200SuggestionsItemKind:
    if value in DRY_RUN_APP_OPEN_API_RESPONSE_200_SUGGESTIONS_ITEM_KIND_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {DRY_RUN_APP_OPEN_API_RESPONSE_200_SUGGESTIONS_ITEM_KIND_VALUES!r}"
    )
