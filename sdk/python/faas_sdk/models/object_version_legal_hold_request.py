from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.object_version_legal_hold import ObjectVersionLegalHold


T = TypeVar("T", bound="ObjectVersionLegalHoldRequest")


@_attrs_define
class ObjectVersionLegalHoldRequest:
    """Stable identity and desired independent legal hold for an owned version."""

    id: UUID
    """Caller-generated UUID v4 for this legal-hold intent; retain it when retrying."""
    legal_hold: ObjectVersionLegalHold
    """Independent exact-version legal hold status."""

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        legal_hold = self.legal_hold.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "id": id,
                "legal_hold": legal_hold,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.object_version_legal_hold import ObjectVersionLegalHold

        d = dict(src_dict)
        id = UUID(d.pop("id"))

        legal_hold = ObjectVersionLegalHold.from_dict(d.pop("legal_hold"))

        object_version_legal_hold_request = cls(
            id=id,
            legal_hold=legal_hold,
        )

        return object_version_legal_hold_request
