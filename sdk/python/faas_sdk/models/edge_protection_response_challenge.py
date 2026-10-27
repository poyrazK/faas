from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="EdgeProtectionResponseChallenge")


@_attrs_define
class EdgeProtectionResponseChallenge:
    """kind=challenge decisions; its 403s also appear in rejections under gate challenge."""

    issued: int
    """Challenge pages served to browser navigations."""
    solved: int
    """Passes earned by solving the proof of work."""
    rejected: int
    """Non-navigation requests answered 403 challenge_required."""
    passed: int
    """Requests let through on a pass from an earlier solve."""
    bypassed: int
    """Requests let through on a verified API key, consumer key or JWT."""
    invalid: int
    """Solutions refused: wrong work or an expired token."""
    raised: int | Unset = UNSET
    """The part of issued served at a difficulty raised under attack."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        issued = self.issued

        solved = self.solved

        rejected = self.rejected

        passed = self.passed

        bypassed = self.bypassed

        invalid = self.invalid

        raised = self.raised

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "issued": issued,
                "solved": solved,
                "rejected": rejected,
                "passed": passed,
                "bypassed": bypassed,
                "invalid": invalid,
            }
        )
        if raised is not UNSET:
            field_dict["raised"] = raised

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        issued = d.pop("issued")

        solved = d.pop("solved")

        rejected = d.pop("rejected")

        passed = d.pop("passed")

        bypassed = d.pop("bypassed")

        invalid = d.pop("invalid")

        raised = d.pop("raised", UNSET)

        edge_protection_response_challenge = cls(
            issued=issued,
            solved=solved,
            rejected=rejected,
            passed=passed,
            bypassed=bypassed,
            invalid=invalid,
            raised=raised,
        )

        edge_protection_response_challenge.additional_properties = d
        return edge_protection_response_challenge

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
