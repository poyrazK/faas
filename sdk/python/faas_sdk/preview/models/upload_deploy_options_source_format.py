from typing import Literal

UploadDeployOptionsSourceFormat = Literal["oci_image_archive"]

UPLOAD_DEPLOY_OPTIONS_SOURCE_FORMAT_VALUES: set[UploadDeployOptionsSourceFormat] = {
    "oci_image_archive",
}


def check_upload_deploy_options_source_format(value: str) -> UploadDeployOptionsSourceFormat:
    if value in UPLOAD_DEPLOY_OPTIONS_SOURCE_FORMAT_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {UPLOAD_DEPLOY_OPTIONS_SOURCE_FORMAT_VALUES!r}")
