from typing import Literal

RuntimeUpgradePreviewResponseChangesItem = Literal["base_bytes", "base_layout", "guest_init", "runtime_source"]

RUNTIME_UPGRADE_PREVIEW_RESPONSE_CHANGES_ITEM_VALUES: set[RuntimeUpgradePreviewResponseChangesItem] = {
    "base_bytes",
    "base_layout",
    "guest_init",
    "runtime_source",
}


def check_runtime_upgrade_preview_response_changes_item(value: str) -> RuntimeUpgradePreviewResponseChangesItem:
    if value in RUNTIME_UPGRADE_PREVIEW_RESPONSE_CHANGES_ITEM_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {RUNTIME_UPGRADE_PREVIEW_RESPONSE_CHANGES_ITEM_VALUES!r}"
    )
