from typing import Literal

DurableEntityRetryRequestTarget = Literal["alarm", "outbox"]

DURABLE_ENTITY_RETRY_REQUEST_TARGET_VALUES: set[DurableEntityRetryRequestTarget] = {
    "alarm",
    "outbox",
}


def check_durable_entity_retry_request_target(value: str) -> DurableEntityRetryRequestTarget:
    if value in DURABLE_ENTITY_RETRY_REQUEST_TARGET_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DURABLE_ENTITY_RETRY_REQUEST_TARGET_VALUES!r}")
