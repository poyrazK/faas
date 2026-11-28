from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.execution_file_input import ExecutionFileInput


T = TypeVar("T", bound="ExecutionTaskBatchItem")


@_attrs_define
class ExecutionTaskBatchItem:
    """A labeled invocation input submitted as part of a task batch."""

    label: str
    input_: Any | Unset = UNSET
    """ JSON input for this batch item. """
    input_files: list[ExecutionFileInput] | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        label = self.label

        input_ = self.input_

        input_files: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.input_files, Unset):
            input_files = []
            for input_files_item_data in self.input_files:
                input_files_item = input_files_item_data.to_dict()
                input_files.append(input_files_item)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "label": label,
            }
        )
        if input_ is not UNSET:
            field_dict["input"] = input_
        if input_files is not UNSET:
            field_dict["input_files"] = input_files

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.execution_file_input import ExecutionFileInput

        d = dict(src_dict)
        label = d.pop("label")

        input_ = d.pop("input", UNSET)

        _input_files = d.pop("input_files", UNSET)
        input_files: list[ExecutionFileInput] | Unset = UNSET
        if _input_files is not UNSET:
            input_files = []
            for input_files_item_data in _input_files:
                input_files_item = ExecutionFileInput.from_dict(input_files_item_data)

                input_files.append(input_files_item)

        execution_task_batch_item = cls(
            label=label,
            input_=input_,
            input_files=input_files,
        )

        return execution_task_batch_item
