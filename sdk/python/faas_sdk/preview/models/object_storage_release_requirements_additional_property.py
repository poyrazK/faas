from typing import Literal

ObjectStorageReleaseRequirementsAdditionalProperty = Literal["read", "read_write", "write"]

OBJECT_STORAGE_RELEASE_REQUIREMENTS_ADDITIONAL_PROPERTY_VALUES: set[
    ObjectStorageReleaseRequirementsAdditionalProperty
] = {
    "read",
    "read_write",
    "write",
}


def check_object_storage_release_requirements_additional_property(
    value: str,
) -> ObjectStorageReleaseRequirementsAdditionalProperty:
    if value in OBJECT_STORAGE_RELEASE_REQUIREMENTS_ADDITIONAL_PROPERTY_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {OBJECT_STORAGE_RELEASE_REQUIREMENTS_ADDITIONAL_PROPERTY_VALUES!r}"
    )
