from typing import Literal

CrashCaptureResponseTrigger = Literal["http_5xx", "manual"]

CRASH_CAPTURE_RESPONSE_TRIGGER_VALUES: set[CrashCaptureResponseTrigger] = {
    "http_5xx",
    "manual",
}


def check_crash_capture_response_trigger(value: str) -> CrashCaptureResponseTrigger:
    if value in CRASH_CAPTURE_RESPONSE_TRIGGER_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {CRASH_CAPTURE_RESPONSE_TRIGGER_VALUES!r}")
