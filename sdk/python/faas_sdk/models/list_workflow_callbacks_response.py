from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.workflow_callback_response import WorkflowCallbackResponse


T = TypeVar("T", bound="ListWorkflowCallbacksResponse")


@_attrs_define
class ListWorkflowCallbacksResponse:
    """Callback handles declared in this run's workflow snapshot."""

    callbacks: list[WorkflowCallbackResponse]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        callbacks = []
        for callbacks_item_data in self.callbacks:
            callbacks_item = callbacks_item_data.to_dict()
            callbacks.append(callbacks_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "callbacks": callbacks,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.workflow_callback_response import WorkflowCallbackResponse

        d = dict(src_dict)
        callbacks = []
        _callbacks = d.pop("callbacks")
        for callbacks_item_data in _callbacks:
            callbacks_item = WorkflowCallbackResponse.from_dict(callbacks_item_data)

            callbacks.append(callbacks_item)

        list_workflow_callbacks_response = cls(
            callbacks=callbacks,
        )

        list_workflow_callbacks_response.additional_properties = d
        return list_workflow_callbacks_response

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
