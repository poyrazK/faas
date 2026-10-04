from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.object_version_legal_hold import ObjectVersionLegalHold


T = TypeVar("T", bound="ObjectVersionLegalHoldResult")


@_attrs_define
class ObjectVersionLegalHoldResult:
    """Selected public version and its observed independent legal hold."""

    version_id: str
    legal_hold: ObjectVersionLegalHold
    """Independent exact-version legal hold status."""

    def to_dict(self) -> dict[str, Any]:
        version_id = self.version_id

        legal_hold = self.legal_hold.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "version_id": version_id,
                "legal_hold": legal_hold,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.object_version_legal_hold import ObjectVersionLegalHold

        d = dict(src_dict)
        version_id = d.pop("version_id")

        legal_hold = ObjectVersionLegalHold.from_dict(d.pop("legal_hold"))

        object_version_legal_hold_result = cls(
            version_id=version_id,
            legal_hold=legal_hold,
        )

        return object_version_legal_hold_result
