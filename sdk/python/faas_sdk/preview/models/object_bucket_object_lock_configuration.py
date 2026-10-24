from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.object_lock_default_retention import ObjectLockDefaultRetention


T = TypeVar("T", bound="ObjectBucketObjectLockConfiguration")


@_attrs_define
class ObjectBucketObjectLockConfiguration:
    """Native observation may report disabled. PUT requires enabled true. Omitting default_retention clears defaults while
    keeping Object Lock enabled.

    """

    enabled: bool
    default_retention: ObjectLockDefaultRetention | Unset = UNSET
    """A mode with exactly one fixed duration, an event hold duration or both. Null values and an empty default are
    rejected."""

    def to_dict(self) -> dict[str, Any]:
        enabled = self.enabled

        default_retention: dict[str, Any] | Unset = UNSET
        if not isinstance(self.default_retention, Unset):
            default_retention = self.default_retention.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "enabled": enabled,
            }
        )
        if default_retention is not UNSET:
            field_dict["default_retention"] = default_retention

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.object_lock_default_retention import ObjectLockDefaultRetention

        d = dict(src_dict)
        enabled = d.pop("enabled")

        _default_retention = d.pop("default_retention", UNSET)
        default_retention: ObjectLockDefaultRetention | Unset
        if isinstance(_default_retention, Unset):
            default_retention = UNSET
        else:
            default_retention = ObjectLockDefaultRetention.from_dict(_default_retention)

        object_bucket_object_lock_configuration = cls(
            enabled=enabled,
            default_retention=default_retention,
        )

        return object_bucket_object_lock_configuration
