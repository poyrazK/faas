from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="ServiceReliabilityPolicy")


@_attrs_define
class ServiceReliabilityPolicy:
    """Caller-owned timeout and retry settings for one declared internal dependency. Omitted numeric fields inherit gateway
    defaults; max_attempts=1 disables retry. POST/PATCH replay also requires an Idempotency-Key honored by the target.

    """

    timeout_ms: int | Unset = UNSET
    max_attempts: int | Unset = UNSET
    min_remaining_ms: int | Unset = UNSET
    retry_budget_percent: int | Unset = UNSET
    allow_non_idempotent: bool | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        timeout_ms = self.timeout_ms

        max_attempts = self.max_attempts

        min_remaining_ms = self.min_remaining_ms

        retry_budget_percent = self.retry_budget_percent

        allow_non_idempotent = self.allow_non_idempotent

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if timeout_ms is not UNSET:
            field_dict["timeout_ms"] = timeout_ms
        if max_attempts is not UNSET:
            field_dict["max_attempts"] = max_attempts
        if min_remaining_ms is not UNSET:
            field_dict["min_remaining_ms"] = min_remaining_ms
        if retry_budget_percent is not UNSET:
            field_dict["retry_budget_percent"] = retry_budget_percent
        if allow_non_idempotent is not UNSET:
            field_dict["allow_non_idempotent"] = allow_non_idempotent

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        timeout_ms = d.pop("timeout_ms", UNSET)

        max_attempts = d.pop("max_attempts", UNSET)

        min_remaining_ms = d.pop("min_remaining_ms", UNSET)

        retry_budget_percent = d.pop("retry_budget_percent", UNSET)

        allow_non_idempotent = d.pop("allow_non_idempotent", UNSET)

        service_reliability_policy = cls(
            timeout_ms=timeout_ms,
            max_attempts=max_attempts,
            min_remaining_ms=min_remaining_ms,
            retry_budget_percent=retry_budget_percent,
            allow_non_idempotent=allow_non_idempotent,
        )

        service_reliability_policy.additional_properties = d
        return service_reliability_policy

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
