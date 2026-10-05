from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="ControlApplicationStandardOperationRequest")


@_attrs_define
class ControlApplicationStandardOperationRequest:
    """Current operation timestamp required to pause, resume or abort retained rollout intent."""

    expected_updated_at: datetime.datetime
    """Exact nonzero updated_at from the current operation, with at most microsecond precision; never round a stale
    token."""

    def to_dict(self) -> dict[str, Any]:
        expected_updated_at = self.expected_updated_at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "expected_updated_at": expected_updated_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        expected_updated_at = datetime.datetime.fromisoformat(d.pop("expected_updated_at"))

        control_application_standard_operation_request = cls(
            expected_updated_at=expected_updated_at,
        )

        return control_application_standard_operation_request
