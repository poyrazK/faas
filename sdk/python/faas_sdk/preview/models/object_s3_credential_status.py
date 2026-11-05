from typing import Literal

ObjectS3CredentialStatus = Literal["active", "revoked"]

OBJECT_S3_CREDENTIAL_STATUS_VALUES: set[ObjectS3CredentialStatus] = {
    "active",
    "revoked",
}


def check_object_s3_credential_status(value: str) -> ObjectS3CredentialStatus:
    if value in OBJECT_S3_CREDENTIAL_STATUS_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBJECT_S3_CREDENTIAL_STATUS_VALUES!r}")
