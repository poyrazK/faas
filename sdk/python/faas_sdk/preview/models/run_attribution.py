from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="RunAttribution")


@_attrs_define
class RunAttribution:
    """Verified customer and Agent references recorded with a Run."""

    customer_ref: str
    agent_ref: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        customer_ref = self.customer_ref

        agent_ref = self.agent_ref

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "customer_ref": customer_ref,
            }
        )
        if agent_ref is not UNSET:
            field_dict["agent_ref"] = agent_ref

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        customer_ref = d.pop("customer_ref")

        agent_ref = d.pop("agent_ref", UNSET)

        run_attribution = cls(
            customer_ref=customer_ref,
            agent_ref=agent_ref,
        )

        run_attribution.additional_properties = d
        return run_attribution

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
