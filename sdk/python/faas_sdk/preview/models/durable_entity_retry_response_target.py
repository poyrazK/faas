from typing import Literal

DurableEntityRetryResponseTarget = Literal["alarm", "outbox"]

DURABLE_ENTITY_RETRY_RESPONSE_TARGET_VALUES: set[DurableEntityRetryResponseTarget] = {
    "alarm",
    "outbox",
}


def check_durable_entity_retry_response_target(value: str) -> DurableEntityRetryResponseTarget:
    if value in DURABLE_ENTITY_RETRY_RESPONSE_TARGET_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DURABLE_ENTITY_RETRY_RESPONSE_TARGET_VALUES!r}")
