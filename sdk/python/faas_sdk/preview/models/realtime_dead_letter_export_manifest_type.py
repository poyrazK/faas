from typing import Literal

RealtimeDeadLetterExportManifestType = Literal["manifest"]

REALTIME_DEAD_LETTER_EXPORT_MANIFEST_TYPE_VALUES: set[RealtimeDeadLetterExportManifestType] = {
    "manifest",
}


def check_realtime_dead_letter_export_manifest_type(value: str) -> RealtimeDeadLetterExportManifestType:
    if value in REALTIME_DEAD_LETTER_EXPORT_MANIFEST_TYPE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {REALTIME_DEAD_LETTER_EXPORT_MANIFEST_TYPE_VALUES!r}")
