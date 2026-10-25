from typing import Literal

DurableEntityRestorePreviewSchemaRelation = Literal["newer", "older", "same", "unknown"]

DURABLE_ENTITY_RESTORE_PREVIEW_SCHEMA_RELATION_VALUES: set[DurableEntityRestorePreviewSchemaRelation] = {
    "newer",
    "older",
    "same",
    "unknown",
}


def check_durable_entity_restore_preview_schema_relation(value: str) -> DurableEntityRestorePreviewSchemaRelation:
    if value in DURABLE_ENTITY_RESTORE_PREVIEW_SCHEMA_RELATION_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {DURABLE_ENTITY_RESTORE_PREVIEW_SCHEMA_RELATION_VALUES!r}"
    )
