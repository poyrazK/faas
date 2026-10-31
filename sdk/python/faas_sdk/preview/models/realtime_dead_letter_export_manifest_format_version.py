from typing import Literal

RealtimeDeadLetterExportManifestFormatVersion = Literal[1]

REALTIME_DEAD_LETTER_EXPORT_MANIFEST_FORMAT_VERSION_VALUES: set[RealtimeDeadLetterExportManifestFormatVersion] = {
    1,
}


def check_realtime_dead_letter_export_manifest_format_version(
    value: int,
) -> RealtimeDeadLetterExportManifestFormatVersion:
    if value in REALTIME_DEAD_LETTER_EXPORT_MANIFEST_FORMAT_VERSION_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {REALTIME_DEAD_LETTER_EXPORT_MANIFEST_FORMAT_VERSION_VALUES!r}"
    )
