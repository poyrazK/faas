from typing import Literal

ObjectEncryptionCapabilitiesAlgorithmsItem = Literal["AES256", "aws:kms", "aws:kms:dsse"]

OBJECT_ENCRYPTION_CAPABILITIES_ALGORITHMS_ITEM_VALUES: set[ObjectEncryptionCapabilitiesAlgorithmsItem] = {
    "AES256",
    "aws:kms",
    "aws:kms:dsse",
}


def check_object_encryption_capabilities_algorithms_item(value: str) -> ObjectEncryptionCapabilitiesAlgorithmsItem:
    if value in OBJECT_ENCRYPTION_CAPABILITIES_ALGORITHMS_ITEM_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OBJECT_ENCRYPTION_CAPABILITIES_ALGORITHMS_ITEM_VALUES!r}"
    )
