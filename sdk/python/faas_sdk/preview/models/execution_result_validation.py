from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.execution_result_validation_message import (
    ExecutionResultValidationMessage,
    check_execution_result_validation_message,
)
from ..models.execution_result_validation_status import (
    ExecutionResultValidationStatus,
    check_execution_result_validation_status,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="ExecutionResultValidation")


@_attrs_define
class ExecutionResultValidation:
    """Host validation of a successful result. Invalid contracts preserve execution status, original result, artifacts, and
    usage; dependent workflows fail and CLI waits return nonzero.

    """

    status: ExecutionResultValidationStatus
    message: ExecutionResultValidationMessage | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        status: str = self.status

        message: str | Unset = UNSET
        if not isinstance(self.message, Unset):
            message = self.message

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "status": status,
            }
        )
        if message is not UNSET:
            field_dict["message"] = message

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        status = check_execution_result_validation_status(d.pop("status"))

        _message = d.pop("message", UNSET)
        message: ExecutionResultValidationMessage | Unset
        if isinstance(_message, Unset):
            message = UNSET
        else:
            message = check_execution_result_validation_message(_message)

        execution_result_validation = cls(
            status=status,
            message=message,
        )

        return execution_result_validation
