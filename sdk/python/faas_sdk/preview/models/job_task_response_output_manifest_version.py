from typing import Literal

JobTaskResponseOutputManifestVersion = Literal[1]

JOB_TASK_RESPONSE_OUTPUT_MANIFEST_VERSION_VALUES: set[JobTaskResponseOutputManifestVersion] = {
    1,
}


def check_job_task_response_output_manifest_version(value: int) -> JobTaskResponseOutputManifestVersion:
    if value in JOB_TASK_RESPONSE_OUTPUT_MANIFEST_VERSION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {JOB_TASK_RESPONSE_OUTPUT_MANIFEST_VERSION_VALUES!r}")
