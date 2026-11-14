from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.tool_invocation_record import ToolInvocationRecord


T = TypeVar("T", bound="ToolInvocationReceipt")


@_attrs_define
class ToolInvocationReceipt:
    """Invocation transition receipt; creation and dispatch flags are fresh transition facts and are false on duplicates."""

    created: bool
    dispatch_granted: bool
    record: ToolInvocationRecord
    """Account-owned single-dispatch fact; cancellation after dispatch does not establish remote rollback or
    successful cancellation."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        created = self.created

        dispatch_granted = self.dispatch_granted

        record = self.record.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "created": created,
                "dispatch_granted": dispatch_granted,
                "record": record,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.tool_invocation_record import ToolInvocationRecord

        d = dict(src_dict)
        created = d.pop("created")

        dispatch_granted = d.pop("dispatch_granted")

        record = ToolInvocationRecord.from_dict(d.pop("record"))

        tool_invocation_receipt = cls(
            created=created,
            dispatch_granted=dispatch_granted,
            record=record,
        )

        tool_invocation_receipt.additional_properties = d
        return tool_invocation_receipt

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
