from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="InvokeRequestRetryPolicyType0")


@_attrs_define
class InvokeRequestRetryPolicyType0:
    """ADR-134 PR-B. Per-row retry curve override. Shape mirrors dispatch.RetryPolicy: { max_attempts, base_seconds,
    max_seconds, jitter_seconds }.

    """

    max_attempts: int | Unset = UNSET
    base_seconds: float | Unset = UNSET
    max_seconds: float | Unset = UNSET
    jitter_seconds: float | Unset = UNSET
    """Fraction (0..1). 0.2 means ±20%."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        max_attempts = self.max_attempts

        base_seconds = self.base_seconds

        max_seconds = self.max_seconds

        jitter_seconds = self.jitter_seconds

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if max_attempts is not UNSET:
            field_dict["max_attempts"] = max_attempts
        if base_seconds is not UNSET:
            field_dict["base_seconds"] = base_seconds
        if max_seconds is not UNSET:
            field_dict["max_seconds"] = max_seconds
        if jitter_seconds is not UNSET:
            field_dict["jitter_seconds"] = jitter_seconds

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        max_attempts = d.pop("max_attempts", UNSET)

        base_seconds = d.pop("base_seconds", UNSET)

        max_seconds = d.pop("max_seconds", UNSET)

        jitter_seconds = d.pop("jitter_seconds", UNSET)

        invoke_request_retry_policy_type_0 = cls(
            max_attempts=max_attempts,
            base_seconds=base_seconds,
            max_seconds=max_seconds,
            jitter_seconds=jitter_seconds,
        )

        invoke_request_retry_policy_type_0.additional_properties = d
        return invoke_request_retry_policy_type_0

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
