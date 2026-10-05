from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="ObjectLockCapabilities")


@_attrs_define
class ObjectLockCapabilities:
    """Enrolled bucket configuration, default event holds, fixed version retention and legal-hold capabilities without a
    native health check.

    """

    bucket_configuration: bool
    default_event_hold: bool
    version_retention: bool
    version_legal_hold: bool

    def to_dict(self) -> dict[str, Any]:
        bucket_configuration = self.bucket_configuration

        default_event_hold = self.default_event_hold

        version_retention = self.version_retention

        version_legal_hold = self.version_legal_hold

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "bucket_configuration": bucket_configuration,
                "default_event_hold": default_event_hold,
                "version_retention": version_retention,
                "version_legal_hold": version_legal_hold,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        bucket_configuration = d.pop("bucket_configuration")

        default_event_hold = d.pop("default_event_hold")

        version_retention = d.pop("version_retention")

        version_legal_hold = d.pop("version_legal_hold")

        object_lock_capabilities = cls(
            bucket_configuration=bucket_configuration,
            default_event_hold=default_event_hold,
            version_retention=version_retention,
            version_legal_hold=version_legal_hold,
        )

        return object_lock_capabilities
