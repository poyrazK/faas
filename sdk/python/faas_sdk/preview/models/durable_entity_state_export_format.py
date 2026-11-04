from typing import Literal

DurableEntityStateExportFormat = Literal[1]

DURABLE_ENTITY_STATE_EXPORT_FORMAT_VALUES: set[DurableEntityStateExportFormat] = {
    1,
}


def check_durable_entity_state_export_format(value: int) -> DurableEntityStateExportFormat:
    if value in DURABLE_ENTITY_STATE_EXPORT_FORMAT_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DURABLE_ENTITY_STATE_EXPORT_FORMAT_VALUES!r}")
