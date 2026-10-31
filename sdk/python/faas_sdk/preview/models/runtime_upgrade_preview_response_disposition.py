from typing import Literal

RuntimeUpgradePreviewResponseDisposition = Literal["blocked", "no_change", "review_required"]

RUNTIME_UPGRADE_PREVIEW_RESPONSE_DISPOSITION_VALUES: set[RuntimeUpgradePreviewResponseDisposition] = {
    "blocked",
    "no_change",
    "review_required",
}


def check_runtime_upgrade_preview_response_disposition(value: str) -> RuntimeUpgradePreviewResponseDisposition:
    if value in RUNTIME_UPGRADE_PREVIEW_RESPONSE_DISPOSITION_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {RUNTIME_UPGRADE_PREVIEW_RESPONSE_DISPOSITION_VALUES!r}"
    )
