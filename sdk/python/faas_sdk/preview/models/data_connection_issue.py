from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.data_connection_issue_state import DataConnectionIssueState, check_data_connection_issue_state
from ..types import UNSET, Unset

T = TypeVar("T", bound="DataConnectionIssue")


@_attrs_define
class DataConnectionIssue:
    """Stable compatibility code and sanitized guidance; contains neither credentials nor raw database errors."""

    code: str
    state: DataConnectionIssueState
    message: str
    remediation: str | Unset = UNSET
    column: str | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        code = self.code

        state: str = self.state

        message = self.message

        remediation = self.remediation

        column = self.column

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "code": code,
                "state": state,
                "message": message,
            }
        )
        if remediation is not UNSET:
            field_dict["remediation"] = remediation
        if column is not UNSET:
            field_dict["column"] = column

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        code = d.pop("code")

        state = check_data_connection_issue_state(d.pop("state"))

        message = d.pop("message")

        remediation = d.pop("remediation", UNSET)

        column = d.pop("column", UNSET)

        data_connection_issue = cls(
            code=code,
            state=state,
            message=message,
            remediation=remediation,
            column=column,
        )

        return data_connection_issue
