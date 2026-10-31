from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.egress_flow_log_entry import EgressFlowLogEntry


T = TypeVar("T", bound="EgressFlowLogResponse")


@_attrs_define
class EgressFlowLogResponse:
    """Egress flow log rows, newest first. truncated means the page limit was reached."""

    flows: list[EgressFlowLogEntry]
    truncated: bool
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        flows = []
        for flows_item_data in self.flows:
            flows_item = flows_item_data.to_dict()
            flows.append(flows_item)

        truncated = self.truncated

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "flows": flows,
                "truncated": truncated,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.egress_flow_log_entry import EgressFlowLogEntry

        d = dict(src_dict)
        flows = []
        _flows = d.pop("flows")
        for flows_item_data in _flows:
            flows_item = EgressFlowLogEntry.from_dict(flows_item_data)

            flows.append(flows_item)

        truncated = d.pop("truncated")

        egress_flow_log_response = cls(
            flows=flows,
            truncated=truncated,
        )

        egress_flow_log_response.additional_properties = d
        return egress_flow_log_response

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
