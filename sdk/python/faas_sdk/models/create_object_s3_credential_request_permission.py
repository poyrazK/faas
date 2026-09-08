from typing import Literal

CreateObjectS3CredentialRequestPermission = Literal["read", "read_write", "write"]

CREATE_OBJECT_S3_CREDENTIAL_REQUEST_PERMISSION_VALUES: set[CreateObjectS3CredentialRequestPermission] = {
    "read",
    "read_write",
    "write",
}


def check_create_object_s3_credential_request_permission(value: str) -> CreateObjectS3CredentialRequestPermission:
    if value in CREATE_OBJECT_S3_CREDENTIAL_REQUEST_PERMISSION_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {CREATE_OBJECT_S3_CREDENTIAL_REQUEST_PERMISSION_VALUES!r}"
    )
