from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.execution_pipeline_completion import ExecutionPipelineCompletion


T = TypeVar("T", bound="ExecutionPipelineReceipt")


@_attrs_define
class ExecutionPipelineReceipt:
    """App-scoped RAP pipeline receipt. Completion is absent while the pipeline is pending. Reads require the owning
    endpoint and a live original thread grant.

    """

    id: UUID
    completion: ExecutionPipelineCompletion | Unset = UNSET
    """ Immutable v1 terminal manifest. Metadata and scoped references only; result and artifact bytes retain their
    ordinary Run receipt permissions and expiry. Completion identity is stable across delivery retries and redrive.
    Aggregate wall/CPU time sums every attempt; peak memory is the maximum attempt peak, not simultaneous residency
    or an invoice. """
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        completion: dict[str, Any] | Unset = UNSET
        if not isinstance(self.completion, Unset):
            completion = self.completion.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
            }
        )
        if completion is not UNSET:
            field_dict["completion"] = completion

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.execution_pipeline_completion import ExecutionPipelineCompletion

        d = dict(src_dict)
        id = UUID(d.pop("id"))

        _completion = d.pop("completion", UNSET)
        completion: ExecutionPipelineCompletion | Unset
        if isinstance(_completion, Unset):
            completion = UNSET
        else:
            completion = ExecutionPipelineCompletion.from_dict(_completion)

        execution_pipeline_receipt = cls(
            id=id,
            completion=completion,
        )

        execution_pipeline_receipt.additional_properties = d
        return execution_pipeline_receipt

    @property
    def additional_keys(self) -> list[str]:
        return list(self.additional_properties.keys())

    def __getitem__(self, key: str) -> Any:
        return self.additional_properties[key]

    def __setitem__(self, key: str, value: Any) -> None:
        self.additional_properties[key] = value

    def __delitem__(self, key: str) -> None:
        del self.additional_properties[key]

    def __contains__(self, key: str) -> bool:
        return key in self.additional_properties
