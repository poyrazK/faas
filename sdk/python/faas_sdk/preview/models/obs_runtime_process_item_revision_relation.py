from typing import Literal

ObsRuntimeProcessItemRevisionRelation = Literal["different", "same", "unknown"]

OBS_RUNTIME_PROCESS_ITEM_REVISION_RELATION_VALUES: set[ObsRuntimeProcessItemRevisionRelation] = {
    "different",
    "same",
    "unknown",
}


def check_obs_runtime_process_item_revision_relation(value: str) -> ObsRuntimeProcessItemRevisionRelation:
    if value in OBS_RUNTIME_PROCESS_ITEM_REVISION_RELATION_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OBS_RUNTIME_PROCESS_ITEM_REVISION_RELATION_VALUES!r}"
    )
