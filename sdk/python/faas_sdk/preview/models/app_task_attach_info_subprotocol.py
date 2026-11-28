from typing import Literal

AppTaskAttachInfoSubprotocol = Literal["gregale-app-task-attach-v1"]

APP_TASK_ATTACH_INFO_SUBPROTOCOL_VALUES: set[AppTaskAttachInfoSubprotocol] = {
    "gregale-app-task-attach-v1",
}


def check_app_task_attach_info_subprotocol(value: str) -> AppTaskAttachInfoSubprotocol:
    if value in APP_TASK_ATTACH_INFO_SUBPROTOCOL_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {APP_TASK_ATTACH_INFO_SUBPROTOCOL_VALUES!r}")
