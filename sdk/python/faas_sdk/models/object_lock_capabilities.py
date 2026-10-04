from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="ObjectLockCapabilities")


@_attrs_define
class ObjectLockCapabilities:
    """Enrolled bucket configuration and default event hold capabilities, without a native health check."""

    bucket_configuration: bool
    default_event_hold: bool

    def to_dict(self) -> dict[str, Any]:
        bucket_configuration = self.bucket_configuration

        default_event_hold = self.default_event_hold

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "bucket_configuration": bucket_configuration,
                "default_event_hold": default_event_hold,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        bucket_configuration = d.pop("bucket_configuration")

        default_event_hold = d.pop("default_event_hold")

        object_lock_capabilities = cls(
            bucket_configuration=bucket_configuration,
            default_event_hold=default_event_hold,
        )

        return object_lock_capabilities
