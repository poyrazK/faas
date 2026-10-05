from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.object_version_retention import ObjectVersionRetention


T = TypeVar("T", bound="ObjectVersionRetentionRequest")


@_attrs_define
class ObjectVersionRetentionRequest:
    """Stable identity and fixed or enrolled event hold retention intent for an owned version. ON requires a duration; OFF
    omits duration and requires a previously active hold unless a fixed date is supplied.

    """

    id: UUID
    """Stable caller-generated UUID v4; reuse only for the identical intent."""
    retention: ObjectVersionRetention
    """Verified native retention or a retention intent. An empty object requests a clear. Active fixed retention
    cannot be shortened and active COMPLIANCE cannot be downgraded. Enrolled event hold ON requires one duration;
    OFF omits duration and lets the provider fix the final date from the existing hold. Observed dates and requested
    minimum dates are preserved. Governance bypass is unsupported. Per-write protection remains fixed retention
    only."""

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        retention = self.retention.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "id": id,
                "retention": retention,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.object_version_retention import ObjectVersionRetention

        d = dict(src_dict)
        id = UUID(d.pop("id"))

        retention = ObjectVersionRetention.from_dict(d.pop("retention"))

        object_version_retention_request = cls(
            id=id,
            retention=retention,
        )

        return object_version_retention_request
