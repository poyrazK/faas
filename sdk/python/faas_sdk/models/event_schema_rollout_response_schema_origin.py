from typing import Literal

EventSchemaRolloutResponseSchemaOrigin = Literal["proposed", "registered"]

EVENT_SCHEMA_ROLLOUT_RESPONSE_SCHEMA_ORIGIN_VALUES: set[EventSchemaRolloutResponseSchemaOrigin] = {
    "proposed",
    "registered",
}


def check_event_schema_rollout_response_schema_origin(value: str) -> EventSchemaRolloutResponseSchemaOrigin:
    if value in EVENT_SCHEMA_ROLLOUT_RESPONSE_SCHEMA_ORIGIN_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {EVENT_SCHEMA_ROLLOUT_RESPONSE_SCHEMA_ORIGIN_VALUES!r}"
    )
