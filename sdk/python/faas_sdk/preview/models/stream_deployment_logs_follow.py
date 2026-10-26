from typing import Literal

StreamDeploymentLogsFollow = Literal[0, 1]

STREAM_DEPLOYMENT_LOGS_FOLLOW_VALUES: set[StreamDeploymentLogsFollow] = {
    0,
    1,
}


def check_stream_deployment_logs_follow(value: int) -> StreamDeploymentLogsFollow:
    if value in STREAM_DEPLOYMENT_LOGS_FOLLOW_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {STREAM_DEPLOYMENT_LOGS_FOLLOW_VALUES!r}")
