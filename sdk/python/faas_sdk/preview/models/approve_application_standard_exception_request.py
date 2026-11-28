from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define

from ..models.approve_application_standard_exception_request_field import (
    ApproveApplicationStandardExceptionRequestField,
    check_approve_application_standard_exception_request_field,
)
from ..models.approve_application_standard_exception_request_value_type_1 import (
    ApproveApplicationStandardExceptionRequestValueType1,
    check_approve_application_standard_exception_request_value_type_1,
)

T = TypeVar("T", bound="ApproveApplicationStandardExceptionRequest")


@_attrs_define
class ApproveApplicationStandardExceptionRequest:
    """Time-limited, reasoned exception for one adopted standard field, bound to the enrollment revision."""

    expected_revision: int
    standard_id: UUID
    version: int
    field: ApproveApplicationStandardExceptionRequestField
    value: ApproveApplicationStandardExceptionRequestValueType1 | bool | list[int] | list[str]
    """Non-null value for the selected field, validated by the server against that field and the full inherited
    controls."""
    reason: str
    """Trimmed nonempty UTF-8 reason bounded to 512 bytes."""
    expires_at: datetime.datetime
    """Future expiry within 30 days of server time."""

    def to_dict(self) -> dict[str, Any]:
        expected_revision = self.expected_revision

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

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "expected_revision": expected_revision,
                "standard_id": standard_id,
                "version": version,
                "field": field,
                "value": value,
                "reason": reason,
                "expires_at": expires_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        expected_revision = d.pop("expected_revision")

        standard_id = UUID(d.pop("standard_id"))

        version = d.pop("version")

        field = check_approve_application_standard_exception_request_field(d.pop("field"))

        def _parse_value(
            data: object,
        ) -> ApproveApplicationStandardExceptionRequestValueType1 | bool | list[int] | list[str]:
            try:
                if not isinstance(data, str):
                    raise TypeError()
                value_type_1 = check_approve_application_standard_exception_request_value_type_1(data)

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
            return cast(ApproveApplicationStandardExceptionRequestValueType1 | bool | list[int] | list[str], data)

        value = _parse_value(d.pop("value"))

        reason = d.pop("reason")

        expires_at = datetime.datetime.fromisoformat(d.pop("expires_at"))

        approve_application_standard_exception_request = cls(
            expected_revision=expected_revision,
            standard_id=standard_id,
            version=version,
            field=field,
            value=value,
            reason=reason,
            expires_at=expires_at,
        )

        return approve_application_standard_exception_request
