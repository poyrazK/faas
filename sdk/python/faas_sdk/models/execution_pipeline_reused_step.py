from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="ExecutionPipelineReusedStep")


@_attrs_define
class ExecutionPipelineReusedStep:
    """Successful earlier step and its verified Run reference reused by a rerun."""

    label: str
    run_id: UUID
    output_sha256: str
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        label = self.label

        run_id = str(self.run_id)

        output_sha256 = self.output_sha256

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "label": label,
                "run_id": run_id,
                "output_sha256": output_sha256,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        label = d.pop("label")

        run_id = UUID(d.pop("run_id"))

        output_sha256 = d.pop("output_sha256")

        execution_pipeline_reused_step = cls(
            label=label,
            run_id=run_id,
            output_sha256=output_sha256,
        )

        execution_pipeline_reused_step.additional_properties = d
        return execution_pipeline_reused_step

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
