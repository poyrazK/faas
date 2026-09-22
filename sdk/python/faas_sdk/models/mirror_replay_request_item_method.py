from typing import Literal

MirrorReplayRequestItemMethod = Literal["DELETE", "GET", "HEAD", "OPTIONS", "PATCH", "POST", "PUT"]

MIRROR_REPLAY_REQUEST_ITEM_METHOD_VALUES: set[MirrorReplayRequestItemMethod] = {
    "DELETE",
    "GET",
    "HEAD",
    "OPTIONS",
    "PATCH",
    "POST",
    "PUT",
}


def check_mirror_replay_request_item_method(value: str) -> MirrorReplayRequestItemMethod:
    if value in MIRROR_REPLAY_REQUEST_ITEM_METHOD_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {MIRROR_REPLAY_REQUEST_ITEM_METHOD_VALUES!r}")
