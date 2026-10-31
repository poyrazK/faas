from typing import Literal

DurableEntityRestorePreviewCompatibility = Literal["unverified"]

DURABLE_ENTITY_RESTORE_PREVIEW_COMPATIBILITY_VALUES: set[DurableEntityRestorePreviewCompatibility] = {
    "unverified",
}


def check_durable_entity_restore_preview_compatibility(value: str) -> DurableEntityRestorePreviewCompatibility:
    if value in DURABLE_ENTITY_RESTORE_PREVIEW_COMPATIBILITY_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {DURABLE_ENTITY_RESTORE_PREVIEW_COMPATIBILITY_VALUES!r}"
    )
