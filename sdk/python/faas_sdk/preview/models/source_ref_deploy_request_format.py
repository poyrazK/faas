from typing import Literal

SourceRefDeployRequestFormat = Literal["tarball"]

SOURCE_REF_DEPLOY_REQUEST_FORMAT_VALUES: set[SourceRefDeployRequestFormat] = {
    "tarball",
}


def check_source_ref_deploy_request_format(value: str) -> SourceRefDeployRequestFormat:
    if value in SOURCE_REF_DEPLOY_REQUEST_FORMAT_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {SOURCE_REF_DEPLOY_REQUEST_FORMAT_VALUES!r}")
