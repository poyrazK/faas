from typing import Literal

ObjectS3CredentialSecretAddressingStyle = Literal["path"]

OBJECT_S3_CREDENTIAL_SECRET_ADDRESSING_STYLE_VALUES: set[ObjectS3CredentialSecretAddressingStyle] = {
    "path",
}


def check_object_s3_credential_secret_addressing_style(value: str) -> ObjectS3CredentialSecretAddressingStyle:
    if value in OBJECT_S3_CREDENTIAL_SECRET_ADDRESSING_STYLE_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OBJECT_S3_CREDENTIAL_SECRET_ADDRESSING_STYLE_VALUES!r}"
    )
