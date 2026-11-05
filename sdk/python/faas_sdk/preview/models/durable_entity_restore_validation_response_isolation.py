from typing import Literal

DurableEntityRestoreValidationResponseIsolation = Literal["networkless"]

DURABLE_ENTITY_RESTORE_VALIDATION_RESPONSE_ISOLATION_VALUES: set[DurableEntityRestoreValidationResponseIsolation] = {
    "networkless",
}


def check_durable_entity_restore_validation_response_isolation(
    value: str,
) -> DurableEntityRestoreValidationResponseIsolation:
    if value in DURABLE_ENTITY_RESTORE_VALIDATION_RESPONSE_ISOLATION_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {DURABLE_ENTITY_RESTORE_VALIDATION_RESPONSE_ISOLATION_VALUES!r}"
    )
