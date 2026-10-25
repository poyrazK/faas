from typing import Literal

ActivityActorResponseType = Literal["api_key", "github", "operator", "system", "user"]

ACTIVITY_ACTOR_RESPONSE_TYPE_VALUES: set[ActivityActorResponseType] = {
    "api_key",
    "github",
    "operator",
    "system",
    "user",
}


def check_activity_actor_response_type(value: str) -> ActivityActorResponseType:
    if value in ACTIVITY_ACTOR_RESPONSE_TYPE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ACTIVITY_ACTOR_RESPONSE_TYPE_VALUES!r}")
