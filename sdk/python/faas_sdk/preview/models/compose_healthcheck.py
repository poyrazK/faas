from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="ComposeHealthcheck")


@_attrs_define
class ComposeHealthcheck:
    """Partial Compose override for a prebuilt image HEALTHCHECK. Empty test and zero timing/retry values inherit image
    settings. NONE disables the check. Durations retain nanosecond precision; positive durations must be at least 1ms.

    """

    test: list[str] | Unset = UNSET
    """CMD followed by argv, CMD-SHELL followed by one command, or NONE."""
    interval_ns: int | Unset = UNSET
    timeout_ns: int | Unset = UNSET
    start_period_ns: int | Unset = UNSET
    start_interval_ns: int | Unset = UNSET
    retries: int | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        test: list[str] | Unset = UNSET
        if not isinstance(self.test, Unset):
            test = self.test

        interval_ns = self.interval_ns

        timeout_ns = self.timeout_ns

        start_period_ns = self.start_period_ns

        start_interval_ns = self.start_interval_ns

        retries = self.retries

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if test is not UNSET:
            field_dict["test"] = test
        if interval_ns is not UNSET:
            field_dict["interval_ns"] = interval_ns
        if timeout_ns is not UNSET:
            field_dict["timeout_ns"] = timeout_ns
        if start_period_ns is not UNSET:
            field_dict["start_period_ns"] = start_period_ns
        if start_interval_ns is not UNSET:
            field_dict["start_interval_ns"] = start_interval_ns
        if retries is not UNSET:
            field_dict["retries"] = retries

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        test = cast(list[str], d.pop("test", UNSET))

        interval_ns = d.pop("interval_ns", UNSET)

        timeout_ns = d.pop("timeout_ns", UNSET)

        start_period_ns = d.pop("start_period_ns", UNSET)

        start_interval_ns = d.pop("start_interval_ns", UNSET)

        retries = d.pop("retries", UNSET)

        compose_healthcheck = cls(
            test=test,
            interval_ns=interval_ns,
            timeout_ns=timeout_ns,
            start_period_ns=start_period_ns,
            start_interval_ns=start_interval_ns,
            retries=retries,
        )

        compose_healthcheck.additional_properties = d
        return compose_healthcheck

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
