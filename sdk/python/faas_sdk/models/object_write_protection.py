from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.object_version_legal_hold import ObjectVersionLegalHold
    from ..models.object_version_retention import ObjectVersionRetention


T = TypeVar("T", bound="ObjectWriteProtection")


@_attrs_define
class ObjectWriteProtection:
    """Fixed or enrolled event retention and an independent legal hold for a new object version. Omitted retention inherits
    the immutable admitted bucket default. Event hold ON requires one days or years duration and permits an optional
    minimum date. Event hold OFF on creation requires an explicit fixed date and no duration. Governance bypass is
    unsupported.

    """

    retention: ObjectVersionRetention | Unset = UNSET
    """Verified native retention or a retention intent. An empty object requests a clear. Active fixed retention
    cannot be shortened and active COMPLIANCE cannot be downgraded. Enrolled event hold ON requires one duration;
    OFF omits duration and lets the provider fix the final date from the existing hold. Observed dates and requested
    minimum dates are preserved. Governance bypass is unsupported. For new writes, OFF requires a fixed date; an
    undated OFF is reserved for releasing an existing hold."""
    legal_hold: ObjectVersionLegalHold | Unset = UNSET
    """Independent exact-version legal hold status."""

    def to_dict(self) -> dict[str, Any]:
        retention: dict[str, Any] | Unset = UNSET
        if not isinstance(self.retention, Unset):
            retention = self.retention.to_dict()

        legal_hold: dict[str, Any] | Unset = UNSET
        if not isinstance(self.legal_hold, Unset):
            legal_hold = self.legal_hold.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update({})
        if retention is not UNSET:
            field_dict["retention"] = retention
        if legal_hold is not UNSET:
            field_dict["legal_hold"] = legal_hold

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.object_version_legal_hold import ObjectVersionLegalHold
        from ..models.object_version_retention import ObjectVersionRetention

        d = dict(src_dict)
        _retention = d.pop("retention", UNSET)
        retention: ObjectVersionRetention | Unset
        if isinstance(_retention, Unset):
            retention = UNSET
        else:
            retention = ObjectVersionRetention.from_dict(_retention)

        _legal_hold = d.pop("legal_hold", UNSET)
        legal_hold: ObjectVersionLegalHold | Unset
        if isinstance(_legal_hold, Unset):
            legal_hold = UNSET
        else:
            legal_hold = ObjectVersionLegalHold.from_dict(_legal_hold)

        object_write_protection = cls(
            retention=retention,
            legal_hold=legal_hold,
        )

        return object_write_protection
