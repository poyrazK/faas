from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.create_data_api_contract_task_request_kind import (
    CreateDataAPIContractTaskRequestKind,
    check_create_data_api_contract_task_request_kind,
)

T = TypeVar("T", bound="CreateDataAPIContractTaskRequest")


@_attrs_define
class CreateDataAPIContractTaskRequest:
    """The contract inspection to run."""

    kind: CreateDataAPIContractTaskRequestKind
    """`types` (TypeScript contract), `validators` (input validators) or `schema` (JSON contract snapshot)."""

    def to_dict(self) -> dict[str, Any]:
        kind: str = self.kind

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "kind": kind,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        kind = check_create_data_api_contract_task_request_kind(d.pop("kind"))

        create_data_api_contract_task_request = cls(
            kind=kind,
        )

        return create_data_api_contract_task_request
