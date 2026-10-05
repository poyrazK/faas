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
    """Stable identity and fixed retention intent for an owned version."""

    id: UUID
    """Stable caller-generated UUID v4; reuse only for the identical intent."""
    retention: ObjectVersionRetention
    """Verified native retention, or a fixed-retention intent. An empty object requests a clear; active retention
    cannot be shortened without bypass, which is unsupported. Event hold fields are observation only for this
    contract."""

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
