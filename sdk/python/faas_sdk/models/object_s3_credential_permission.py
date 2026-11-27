from typing import Literal

ObjectS3CredentialPermission = Literal["read", "read_write", "write"]

OBJECT_S3_CREDENTIAL_PERMISSION_VALUES: set[ObjectS3CredentialPermission] = {
    "read",
    "read_write",
    "write",
}


def check_object_s3_credential_permission(value: str) -> ObjectS3CredentialPermission:
    if value in OBJECT_S3_CREDENTIAL_PERMISSION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBJECT_S3_CREDENTIAL_PERMISSION_VALUES!r}")
