from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="EdgeRuleChallengeAction")


@_attrs_define
class EdgeRuleChallengeAction:
    """Asks browsers on matched routes to prove they run a real browser
    before the request reaches the app. A request
    without a valid pass is answered at the edge: a page load gets a page
    that solves a short proof of work in the browser and sets a signed
    pass cookie, and any other request gets 403 challenge_required.
    Requests with a verified API key, consumer key or JWT subject are
    never challenged. Use log mode to see what a rule would challenge,
    and add a verified_bot missing condition so search crawlers pass.
    Hobby and above. Omitted values are stored as their defaults.

    """

    difficulty: int | Unset = 16
    """Leading zero bits the browser must find in a SHA-256 hash. Each extra bit doubles the work; the default
    takes tens to a few hundred milliseconds."""
    pass_ttl_seconds: int | Unset = 3600
    """How long a solved challenge lets the browser through."""
    max_difficulty: int | Unset = UNSET
    """When above difficulty, the gateway raises the difficulty up to this while the rule serves far more challenge
    pages than usual, two bits (4x the work) per minute, and lowers it again as the load eases. Must be at least
    difficulty. Omitted or equal to difficulty keeps the difficulty fixed."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        difficulty = self.difficulty

        pass_ttl_seconds = self.pass_ttl_seconds

        max_difficulty = self.max_difficulty

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if difficulty is not UNSET:
            field_dict["difficulty"] = difficulty
        if pass_ttl_seconds is not UNSET:
            field_dict["pass_ttl_seconds"] = pass_ttl_seconds
        if max_difficulty is not UNSET:
            field_dict["max_difficulty"] = max_difficulty

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        difficulty = d.pop("difficulty", UNSET)

        pass_ttl_seconds = d.pop("pass_ttl_seconds", UNSET)

        max_difficulty = d.pop("max_difficulty", UNSET)

        edge_rule_challenge_action = cls(
            difficulty=difficulty,
            pass_ttl_seconds=pass_ttl_seconds,
            max_difficulty=max_difficulty,
        )

        edge_rule_challenge_action.additional_properties = d
        return edge_rule_challenge_action

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
