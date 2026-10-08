from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="OperationWorkflowBlocker")


@_attrs_define
class OperationWorkflowBlocker:
    """Public application-reported reason a named target Operation must wait. Reports replace the entire prior blocker list
    at their state revision; these observations do not grant or enforce execution authority.

    """

    code: str
    description: str
    """Public UTF-8 text limited to 512 bytes without control characters."""
    operation: str
    """Target Operation name."""
    first_observed_at: datetime.datetime | Unset = UNSET
    """Optional application observation time at or before the containing report. Upgraded transactional SDKs
    preserve it across repeats of the same target/code until cleared. Omitted means unknown."""

    def to_dict(self) -> dict[str, Any]:
        code = self.code

        description = self.description

        operation = self.operation

        first_observed_at: str | Unset = UNSET
        if not isinstance(self.first_observed_at, Unset):
            first_observed_at = self.first_observed_at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "code": code,
                "description": description,
                "operation": operation,
            }
        )
        if first_observed_at is not UNSET:
            field_dict["first_observed_at"] = first_observed_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        code = d.pop("code")

        description = d.pop("description")

        operation = d.pop("operation")

        _first_observed_at = d.pop("first_observed_at", UNSET)
        first_observed_at: datetime.datetime | Unset
        if isinstance(_first_observed_at, Unset):
            first_observed_at = UNSET
        else:
            first_observed_at = datetime.datetime.fromisoformat(_first_observed_at)

        operation_workflow_blocker = cls(
            code=code,
            description=description,
            operation=operation,
            first_observed_at=first_observed_at,
        )

        return operation_workflow_blocker
