from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.pre_auth_rate_limit_config_mode import PreAuthRateLimitConfigMode, check_pre_auth_rate_limit_config_mode
from ..types import UNSET, Unset

T = TypeVar("T", bound="PreAuthRateLimitConfig")


@_attrs_define
class PreAuthRateLimitConfig:
    """Optional per-source gateway limit evaluated before consumer-key lookup, JWT verification, and VM wake. A gateway
    replica enforces its own buckets; the existing app/account limits remain aggregate ceilings. Observe mode records
    threshold crossings without rejecting requests.

    """

    mode: PreAuthRateLimitConfigMode
    requests_per_second: int | Unset = UNSET
    """Required in observe/enforce mode and bounded by the app plan's request rate."""
    burst: int | Unset = UNSET
    """Required in observe/enforce mode and bounded by the app plan's request burst."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        mode: str = self.mode

        requests_per_second = self.requests_per_second

        burst = self.burst

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "mode": mode,
            }
        )
        if requests_per_second is not UNSET:
            field_dict["requests_per_second"] = requests_per_second
        if burst is not UNSET:
            field_dict["burst"] = burst

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        mode = check_pre_auth_rate_limit_config_mode(d.pop("mode"))

        requests_per_second = d.pop("requests_per_second", UNSET)

        burst = d.pop("burst", UNSET)

        pre_auth_rate_limit_config = cls(
            mode=mode,
            requests_per_second=requests_per_second,
            burst=burst,
        )

        pre_auth_rate_limit_config.additional_properties = d
        return pre_auth_rate_limit_config

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
