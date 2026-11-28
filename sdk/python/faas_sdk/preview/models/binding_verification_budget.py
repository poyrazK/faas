from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="BindingVerificationBudget")


@_attrs_define
class BindingVerificationBudget:
    """Public observation of shared capacity. Underlying unfinished resources remain counted after diagnostic expiry or
    cancellation requests. No admission authority.

    """

    run_id: UUID
    max_concurrency: int
    active_resources: int
    waiting_steps: int
    full: bool
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        run_id = str(self.run_id)

        max_concurrency = self.max_concurrency

        active_resources = self.active_resources

        waiting_steps = self.waiting_steps

        full = self.full

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "run_id": run_id,
                "max_concurrency": max_concurrency,
                "active_resources": active_resources,
                "waiting_steps": waiting_steps,
                "full": full,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        run_id = UUID(d.pop("run_id"))

        max_concurrency = d.pop("max_concurrency")

        active_resources = d.pop("active_resources")

        waiting_steps = d.pop("waiting_steps")

        full = d.pop("full")

        binding_verification_budget = cls(
            run_id=run_id,
            max_concurrency=max_concurrency,
            active_resources=active_resources,
            waiting_steps=waiting_steps,
            full=full,
        )

        binding_verification_budget.additional_properties = d
        return binding_verification_budget

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
