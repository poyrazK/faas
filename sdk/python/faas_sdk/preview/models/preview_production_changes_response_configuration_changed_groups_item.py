from typing import Literal

PreviewProductionChangesResponseConfigurationChangedGroupsItem = Literal[
    "policies", "resources", "routing", "runtime", "scaling"
]

PREVIEW_PRODUCTION_CHANGES_RESPONSE_CONFIGURATION_CHANGED_GROUPS_ITEM_VALUES: set[
    PreviewProductionChangesResponseConfigurationChangedGroupsItem
] = {
    "policies",
    "resources",
    "routing",
    "runtime",
    "scaling",
}


def check_preview_production_changes_response_configuration_changed_groups_item(
    value: str,
) -> PreviewProductionChangesResponseConfigurationChangedGroupsItem:
    if value in PREVIEW_PRODUCTION_CHANGES_RESPONSE_CONFIGURATION_CHANGED_GROUPS_ITEM_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {PREVIEW_PRODUCTION_CHANGES_RESPONSE_CONFIGURATION_CHANGED_GROUPS_ITEM_VALUES!r}"
    )
