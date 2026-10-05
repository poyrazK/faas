from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.object_version_protection_kind import ObjectVersionProtectionKind, check_object_version_protection_kind
from ..models.object_version_protection_last_error_code import (
    ObjectVersionProtectionLastErrorCode,
    check_object_version_protection_last_error_code,
)
from ..models.object_version_protection_state import ObjectVersionProtectionState, check_object_version_protection_state
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.object_version_legal_hold import ObjectVersionLegalHold
    from ..models.object_version_retention import ObjectVersionRetention


T = TypeVar("T", bound="ObjectVersionProtection")


@_attrs_define
class ObjectVersionProtection:
    """Durable public protection receipt containing immutable intent and bounded reconciliation progress."""

    id: UUID
    bucket_id: UUID
    key: str
    version_id: str
    kind: ObjectVersionProtectionKind
    state: ObjectVersionProtectionState
    created_at: datetime.datetime
    updated_at: datetime.datetime
    retention: ObjectVersionRetention | Unset = UNSET
    """Verified native retention, or a fixed-retention intent. An empty object requests a clear; active retention
    cannot be shortened without bypass, which is unsupported. Event hold fields are observation only for this
    contract."""
    legal_hold: ObjectVersionLegalHold | Unset = UNSET
    """Independent exact-version legal hold status."""
    last_error_code: ObjectVersionProtectionLastErrorCode | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        bucket_id = str(self.bucket_id)

        key = self.key

        version_id = self.version_id

        kind: str = self.kind

        state: str = self.state

        created_at = self.created_at.isoformat()

        updated_at = self.updated_at.isoformat()

        retention: dict[str, Any] | Unset = UNSET
        if not isinstance(self.retention, Unset):
            retention = self.retention.to_dict()

        legal_hold: dict[str, Any] | Unset = UNSET
        if not isinstance(self.legal_hold, Unset):
            legal_hold = self.legal_hold.to_dict()

        last_error_code: str | Unset = UNSET
        if not isinstance(self.last_error_code, Unset):
            last_error_code = self.last_error_code

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "id": id,
                "bucket_id": bucket_id,
                "key": key,
                "version_id": version_id,
                "kind": kind,
                "state": state,
                "created_at": created_at,
                "updated_at": updated_at,
            }
        )
        if retention is not UNSET:
            field_dict["retention"] = retention
        if legal_hold is not UNSET:
            field_dict["legal_hold"] = legal_hold
        if last_error_code is not UNSET:
            field_dict["last_error_code"] = last_error_code

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.object_version_legal_hold import ObjectVersionLegalHold
        from ..models.object_version_retention import ObjectVersionRetention

        d = dict(src_dict)
        id = UUID(d.pop("id"))

        bucket_id = UUID(d.pop("bucket_id"))

        key = d.pop("key")

        version_id = d.pop("version_id")

        kind = check_object_version_protection_kind(d.pop("kind"))

        state = check_object_version_protection_state(d.pop("state"))

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

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

        _last_error_code = d.pop("last_error_code", UNSET)
        last_error_code: ObjectVersionProtectionLastErrorCode | Unset
        if isinstance(_last_error_code, Unset):
            last_error_code = UNSET
        else:
            last_error_code = check_object_version_protection_last_error_code(_last_error_code)

        object_version_protection = cls(
            id=id,
            bucket_id=bucket_id,
            key=key,
            version_id=version_id,
            kind=kind,
            state=state,
            created_at=created_at,
            updated_at=updated_at,
            retention=retention,
            legal_hold=legal_hold,
            last_error_code=last_error_code,
        )

        return object_version_protection
