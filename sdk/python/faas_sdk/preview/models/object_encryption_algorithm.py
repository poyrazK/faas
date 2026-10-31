from typing import Literal

ObjectEncryptionAlgorithm = Literal["AES256", "aws:kms", "aws:kms:dsse"]

OBJECT_ENCRYPTION_ALGORITHM_VALUES: set[ObjectEncryptionAlgorithm] = {
    "AES256",
    "aws:kms",
    "aws:kms:dsse",
}


def check_object_encryption_algorithm(value: str) -> ObjectEncryptionAlgorithm:
    if value in OBJECT_ENCRYPTION_ALGORITHM_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {OBJECT_ENCRYPTION_ALGORITHM_VALUES!r}")
