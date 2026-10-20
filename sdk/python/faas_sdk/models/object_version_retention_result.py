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
    """Verified native retention or a retention intent. An empty object requests a clear. Active fixed retention
    cannot be shortened and active COMPLIANCE cannot be downgraded. Enrolled event hold ON requires one duration;
    OFF omits duration and lets the provider fix the final date from the existing hold. Observed dates and requested
    minimum dates are preserved. Governance bypass is unsupported. For new writes, OFF requires a fixed date; an
    undated OFF is reserved for releasing an existing hold."""

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
