from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.hosted_task_binding_result_name import HostedTaskBindingResultName, check_hosted_task_binding_result_name
from ..models.hosted_task_binding_result_status import (
    HostedTaskBindingResultStatus,
    check_hosted_task_binding_result_status,
)

T = TypeVar("T", bound="HostedTaskBindingResult")


@_attrs_define
class HostedTaskBindingResult:
    """One fixed-name bounded qualification outcome containing no native database facts or secret material."""

    name: HostedTaskBindingResultName
    status: HostedTaskBindingResultStatus

    def to_dict(self) -> dict[str, Any]:
        name: str = self.name

        status: str = self.status

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "name": name,
                "status": status,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        name = check_hosted_task_binding_result_name(d.pop("name"))

        status = check_hosted_task_binding_result_status(d.pop("status"))

        hosted_task_binding_result = cls(
            name=name,
            status=status,
        )

        return hosted_task_binding_result
