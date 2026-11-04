from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.release_gate import ReleaseGate


T = TypeVar("T", bound="ListReleaseGatesResponse")


@_attrs_define
class ListReleaseGatesResponse:
    """An app's release gates, by name."""

    gates: list[ReleaseGate]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        gates = []
        for gates_item_data in self.gates:
            gates_item = gates_item_data.to_dict()
            gates.append(gates_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "gates": gates,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.release_gate import ReleaseGate

        d = dict(src_dict)
        gates = []
        _gates = d.pop("gates")
        for gates_item_data in _gates:
            gates_item = ReleaseGate.from_dict(gates_item_data)

            gates.append(gates_item)

        list_release_gates_response = cls(
            gates=gates,
        )

        list_release_gates_response.additional_properties = d
        return list_release_gates_response

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
