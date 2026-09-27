from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="PreAuthFailedResponseLimit")


@_attrs_define
class PreAuthFailedResponseLimit:
    """Optional per-source budget spent only by selected proxied application 4xx responses. When statuses is omitted, 401
    and 403 are counted. In enforce mode, subsequent requests are rejected before authentication and wake after this
    budget is exhausted.

    """

    failures_per_minute: int
    """Continuous token refill per minute; no greater than the parent route's requests_per_second times 60."""
    burst: int
    """Maximum consecutive failed responses; no greater than the parent route's burst."""
    statuses: list[int] | Unset = UNSET
    """Selected application response statuses. Defaults to [401, 403]. Only 4xx codes except 429 are supported."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        failures_per_minute = self.failures_per_minute

        burst = self.burst

        statuses: list[int] | Unset = UNSET
        if not isinstance(self.statuses, Unset):
            statuses = self.statuses

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "failures_per_minute": failures_per_minute,
                "burst": burst,
            }
        )
        if statuses is not UNSET:
            field_dict["statuses"] = statuses

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        failures_per_minute = d.pop("failures_per_minute")

        burst = d.pop("burst")

        statuses = cast(list[int], d.pop("statuses", UNSET))

        pre_auth_failed_response_limit = cls(
            failures_per_minute=failures_per_minute,
            burst=burst,
            statuses=statuses,
        )

        pre_auth_failed_response_limit.additional_properties = d
        return pre_auth_failed_response_limit

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
