from typing import Literal

ObsInputHealthAvailableRolesItem = Literal["control-plane-v1", "single-box-v1"]

OBS_INPUT_HEALTH_AVAILABLE_ROLES_ITEM_VALUES: set[ObsInputHealthAvailableRolesItem] = {
    "control-plane-v1",
    "single-box-v1",
}


def check_obs_input_health_available_roles_item(value: str) -> ObsInputHealthAvailableRolesItem:
    if value in OBS_INPUT_HEALTH_AVAILABLE_ROLES_ITEM_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBS_INPUT_HEALTH_AVAILABLE_ROLES_ITEM_VALUES!r}")
