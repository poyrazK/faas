from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.object_version_retention import ObjectVersionRetention


T = TypeVar("T", bound="ObjectVersionRetentionResult")


@_attrs_define
class ObjectVersionRetentionResult:
    """Selected public version and its observed native retention."""

    version_id: str
    retention: ObjectVersionRetention
    """Verified native retention, or a fixed-retention intent. An empty object requests a clear; active retention
    cannot be shortened without bypass, which is unsupported. Event hold fields are observation only for this
    contract."""

    def to_dict(self) -> dict[str, Any]:
        version_id = self.version_id

        retention = self.retention.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "version_id": version_id,
                "retention": retention,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.object_version_retention import ObjectVersionRetention

        d = dict(src_dict)
        version_id = d.pop("version_id")

        retention = ObjectVersionRetention.from_dict(d.pop("retention"))

        object_version_retention_result = cls(
            version_id=version_id,
            retention=retention,
        )

        return object_version_retention_result
