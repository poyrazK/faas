from typing import Literal

DevPatchPreviewReason = Literal[
    "build_command",
    "full_snapshot",
    "no_live_build",
    "no_source_layer",
    "not_railpack",
    "patch_too_large",
    "plan_unreadable",
    "rebuild_input_changed",
    "source_not_deployed",
    "unsupported_entry",
    "unsupported_source_map",
]

DEV_PATCH_PREVIEW_REASON_VALUES: set[DevPatchPreviewReason] = {
    "build_command",
    "full_snapshot",
    "no_live_build",
    "no_source_layer",
    "not_railpack",
    "patch_too_large",
    "plan_unreadable",
    "rebuild_input_changed",
    "source_not_deployed",
    "unsupported_entry",
    "unsupported_source_map",
}


def check_dev_patch_preview_reason(value: str) -> DevPatchPreviewReason:
    if value in DEV_PATCH_PREVIEW_REASON_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DEV_PATCH_PREVIEW_REASON_VALUES!r}")
