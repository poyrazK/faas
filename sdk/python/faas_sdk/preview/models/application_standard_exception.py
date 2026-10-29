from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define

from ..models.application_standard_exception_field import (
    ApplicationStandardExceptionField,
    check_application_standard_exception_field,
)
from ..models.application_standard_exception_status import (
    ApplicationStandardExceptionStatus,
    check_application_standard_exception_status,
)
from ..models.application_standard_exception_value_type_1 import (
    ApplicationStandardExceptionValueType1,
    check_application_standard_exception_value_type_1,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="ApplicationStandardException")


@_attrs_define
class ApplicationStandardException:
    """Historical approval. Status uses the server as_of timestamp, with revocation taking precedence over expiry;
    applicability depends on current adoption.

    """

    id: UUID
    org_id: UUID
    app_id: UUID
    standard_id: UUID
    version: int
    field: ApplicationStandardExceptionField
    value: ApplicationStandardExceptionValueType1 | bool | list[int] | list[str]
    reason: str
    expires_at: datetime.datetime
    approved_by: UUID
    created_at: datetime.datetime
    status: ApplicationStandardExceptionStatus
    revoked_by: UUID | Unset = UNSET
    revoked_at: datetime.datetime | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        org_id = str(self.org_id)

        app_id = str(self.app_id)

        standard_id = str(self.standard_id)

        version = self.version

        field: str = self.field

        value: bool | list[int] | list[str] | str
        if isinstance(self.value, str):
            value = self.value
        elif isinstance(self.value, list):
            value = self.value

        elif isinstance(self.value, list):
            value = self.value

        else:
            value = self.value

        reason = self.reason

        expires_at = self.expires_at.isoformat()

        approved_by = str(self.approved_by)

        created_at = self.created_at.isoformat()

        status: str = self.status

        revoked_by: str | Unset = UNSET
        if not isinstance(self.revoked_by, Unset):
            revoked_by = str(self.revoked_by)

        revoked_at: str | Unset = UNSET
        if not isinstance(self.revoked_at, Unset):
            revoked_at = self.revoked_at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "id": id,
                "org_id": org_id,
                "app_id": app_id,
                "standard_id": standard_id,
                "version": version,
                "field": field,
                "value": value,
                "reason": reason,
                "expires_at": expires_at,
                "approved_by": approved_by,
                "created_at": created_at,
                "status": status,
            }
        )
        if revoked_by is not UNSET:
            field_dict["revoked_by"] = revoked_by
        if revoked_at is not UNSET:
            field_dict["revoked_at"] = revoked_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = UUID(d.pop("id"))

        org_id = UUID(d.pop("org_id"))

        app_id = UUID(d.pop("app_id"))

        standard_id = UUID(d.pop("standard_id"))

        version = d.pop("version")

        field = check_application_standard_exception_field(d.pop("field"))

        def _parse_value(data: object) -> ApplicationStandardExceptionValueType1 | bool | list[int] | list[str]:
            try:
                if not isinstance(data, str):
                    raise TypeError()
                value_type_1 = check_application_standard_exception_value_type_1(data)

                return value_type_1
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            try:
                if not isinstance(data, list):
                    raise TypeError()
                value_type_2 = cast(list[str], data)

                return value_type_2
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            try:
                if not isinstance(data, list):
                    raise TypeError()
                value_type_3 = cast(list[int], data)

                return value_type_3
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(ApplicationStandardExceptionValueType1 | bool | list[int] | list[str], data)

        value = _parse_value(d.pop("value"))

        reason = d.pop("reason")

        expires_at = datetime.datetime.fromisoformat(d.pop("expires_at"))

        approved_by = UUID(d.pop("approved_by"))

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        status = check_application_standard_exception_status(d.pop("status"))

        _revoked_by = d.pop("revoked_by", UNSET)
        revoked_by: UUID | Unset
        if isinstance(_revoked_by, Unset):
            revoked_by = UNSET
        else:
            revoked_by = UUID(_revoked_by)

        _revoked_at = d.pop("revoked_at", UNSET)
        revoked_at: datetime.datetime | Unset
        if isinstance(_revoked_at, Unset):
            revoked_at = UNSET
        else:
            revoked_at = datetime.datetime.fromisoformat(_revoked_at)

        application_standard_exception = cls(
            id=id,
            org_id=org_id,
            app_id=app_id,
            standard_id=standard_id,
            version=version,
            field=field,
            value=value,
            reason=reason,
            expires_at=expires_at,
            approved_by=approved_by,
            created_at=created_at,
            status=status,
            revoked_by=revoked_by,
            revoked_at=revoked_at,
        )

        return application_standard_exception
