from typing import Literal

RealtimeDeadLetterExportRecordType = Literal["dead_letter"]

REALTIME_DEAD_LETTER_EXPORT_RECORD_TYPE_VALUES: set[RealtimeDeadLetterExportRecordType] = {
    "dead_letter",
}


def check_realtime_dead_letter_export_record_type(value: str) -> RealtimeDeadLetterExportRecordType:
    if value in REALTIME_DEAD_LETTER_EXPORT_RECORD_TYPE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {REALTIME_DEAD_LETTER_EXPORT_RECORD_TYPE_VALUES!r}")
