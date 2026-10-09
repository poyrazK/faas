from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="OperationWorkflowBlockerDuration")


@_attrs_define
class OperationWorkflowBlockerDuration:
    contract_version: int
    operation: str
    code: str
    owner: str
    """Empty means unassigned."""
    observed_seconds: int
    observation_count: int
    ongoing: bool

    def to_dict(self) -> dict[str, Any]:
        contract_version = self.contract_version

        operation = self.operation

        code = self.code

        owner = self.owner

        observed_seconds = self.observed_seconds

        observation_count = self.observation_count

        ongoing = self.ongoing

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "contract_version": contract_version,
                "operation": operation,
                "code": code,
                "owner": owner,
                "observed_seconds": observed_seconds,
                "observation_count": observation_count,
                "ongoing": ongoing,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        contract_version = d.pop("contract_version")

        operation = d.pop("operation")

        code = d.pop("code")

        owner = d.pop("owner")

        observed_seconds = d.pop("observed_seconds")

        observation_count = d.pop("observation_count")

        ongoing = d.pop("ongoing")

        operation_workflow_blocker_duration = cls(
            contract_version=contract_version,
            operation=operation,
            code=code,
            owner=owner,
            observed_seconds=observed_seconds,
            observation_count=observation_count,
            ongoing=ongoing,
        )

        return operation_workflow_blocker_duration
