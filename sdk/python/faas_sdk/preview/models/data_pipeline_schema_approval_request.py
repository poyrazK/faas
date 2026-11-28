from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="DataPipelineSchemaApprovalRequest")


@_attrs_define
class DataPipelineSchemaApprovalRequest:
    """Owner approval of the exact saved source-definition review, or a bounded retry of the already approved backfill and
    refresh workflow.

    """

    expected_revision: int
    change_id: str
    retry: bool | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        expected_revision = self.expected_revision

        change_id = self.change_id

        retry = self.retry

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "expected_revision": expected_revision,
                "change_id": change_id,
            }
        )
        if retry is not UNSET:
            field_dict["retry"] = retry

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        expected_revision = d.pop("expected_revision")

        change_id = d.pop("change_id")

        retry = d.pop("retry", UNSET)

        data_pipeline_schema_approval_request = cls(
            expected_revision=expected_revision,
            change_id=change_id,
            retry=retry,
        )

        return data_pipeline_schema_approval_request
