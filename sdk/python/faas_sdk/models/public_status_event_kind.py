from typing import Literal

PublicStatusEventKind = Literal["incident", "maintenance"]

PUBLIC_STATUS_EVENT_KIND_VALUES: set[PublicStatusEventKind] = {
    "incident",
    "maintenance",
}


def check_public_status_event_kind(value: str) -> PublicStatusEventKind:
    if value in PUBLIC_STATUS_EVENT_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {PUBLIC_STATUS_EVENT_KIND_VALUES!r}")
