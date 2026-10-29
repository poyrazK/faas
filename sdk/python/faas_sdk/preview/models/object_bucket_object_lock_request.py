from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.object_bucket_object_lock_configuration import ObjectBucketObjectLockConfiguration


T = TypeVar("T", bound="ObjectBucketObjectLockRequest")


@_attrs_define
class ObjectBucketObjectLockRequest:
    """An enabled-only permanent bucket configuration request; omitted defaults clear future defaults."""

    configuration: ObjectBucketObjectLockConfiguration
    """Native observation may report disabled. PUT requires enabled true. Omitting default_retention clears
    defaults while keeping Object Lock enabled."""

    def to_dict(self) -> dict[str, Any]:
        configuration = self.configuration.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "configuration": configuration,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.object_bucket_object_lock_configuration import ObjectBucketObjectLockConfiguration

        d = dict(src_dict)
        configuration = ObjectBucketObjectLockConfiguration.from_dict(d.pop("configuration"))

        object_bucket_object_lock_request = cls(
            configuration=configuration,
        )

        return object_bucket_object_lock_request
