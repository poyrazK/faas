from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="TriggerWorkBinding")


@_attrs_define
class TriggerWorkBinding:
    """An external broker trigger's binding to a named app work policy."""

    policy_name: str
    """Named policy in the trigger's app."""
    key: str
    """Scalar dot path into the broker message JSON payload."""
    fairness_key: str | Unset = UNSET
    """Optional scalar dot path for the fairness group."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        policy_name = self.policy_name

        key = self.key

        fairness_key = self.fairness_key

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "policy_name": policy_name,
                "key": key,
            }
        )
        if fairness_key is not UNSET:
            field_dict["fairness_key"] = fairness_key

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        policy_name = d.pop("policy_name")

        key = d.pop("key")

        fairness_key = d.pop("fairness_key", UNSET)

        trigger_work_binding = cls(
            policy_name=policy_name,
            key=key,
            fairness_key=fairness_key,
        )

        trigger_work_binding.additional_properties = d
        return trigger_work_binding

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
