from typing import Literal

ManagedRealtimeSignalResponseMemberId = Literal["backend"]

MANAGED_REALTIME_SIGNAL_RESPONSE_MEMBER_ID_VALUES: set[ManagedRealtimeSignalResponseMemberId] = {
    "backend",
}


def check_managed_realtime_signal_response_member_id(value: str) -> ManagedRealtimeSignalResponseMemberId:
    if value in MANAGED_REALTIME_SIGNAL_RESPONSE_MEMBER_ID_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {MANAGED_REALTIME_SIGNAL_RESPONSE_MEMBER_ID_VALUES!r}"
    )
