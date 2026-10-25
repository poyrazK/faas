from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="EdgeRulePenalty")


@_attrs_define
class EdgeRulePenalty:
    """A rule's penalty box (ADR-20261022105846). Once the rule has refused
    the same client `after` times within `within_seconds`, every request
    from that client to the app is refused with 403 `penalty_box` and
    Retry-After for `seconds`, whichever rule or path it targets. The
    client is its IP address (an IPv6 client is its /64). Boxes are held
    in each gateway node's memory. Allowed on ip, geo, throttle and waf
    (block mode) rules; editing the rule's penalty away, disabling it or
    putting it in log mode releases its clients.

    """

    seconds: int
    """How long a boxed client is refused."""
    after: int | Unset = 1
    """Refusals by this rule that box the client."""
    within_seconds: int | Unset = 60
    """Window the refusals count in."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        seconds = self.seconds

        after = self.after

        within_seconds = self.within_seconds

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "seconds": seconds,
            }
        )
        if after is not UNSET:
            field_dict["after"] = after
        if within_seconds is not UNSET:
            field_dict["within_seconds"] = within_seconds

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        seconds = d.pop("seconds")

        after = d.pop("after", UNSET)

        within_seconds = d.pop("within_seconds", UNSET)

        edge_rule_penalty = cls(
            seconds=seconds,
            after=after,
            within_seconds=within_seconds,
        )

        edge_rule_penalty.additional_properties = d
        return edge_rule_penalty

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
