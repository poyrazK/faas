from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="EdgeAttackModeStatus")


@_attrs_define
class EdgeAttackModeStatus:
    """An app's attack mode. While it is on, every
    request without a challenge pass is challenged: browsers solve a
    short proof of work once and continue, verified search crawlers and
    requests with a verified API key, consumer key or JWT pass straight
    through, and other clients get 403 `challenge_required`. It always
    ends on its own at `expires_at`. Read from the rules, so it is
    current even when the edge-protection counts are degraded.

    """

    active: bool
    """Attack mode is on now."""
    rule_id: str | Unset = UNSET
    """The app-wide challenge rule that carries attack mode (managed_by attack_mode). Still set after attack mode
    ran out, until it is switched on again or off."""
    started_at: datetime.datetime | Unset = UNSET
    """When attack mode was switched on; extending it keeps this time."""
    expires_at: datetime.datetime | Unset = UNSET
    """When attack mode ends"""
    difficulty: int | Unset = UNSET
    """Leading zero bits the browser's proof of work must find."""
    pass_ttl_seconds: int | Unset = UNSET
    """Seconds a browser that solved the attack-mode challenge is let through."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        active = self.active

        rule_id = self.rule_id

        started_at: str | Unset = UNSET
        if not isinstance(self.started_at, Unset):
            started_at = self.started_at.isoformat()

        expires_at: str | Unset = UNSET
        if not isinstance(self.expires_at, Unset):
            expires_at = self.expires_at.isoformat()

        difficulty = self.difficulty

        pass_ttl_seconds = self.pass_ttl_seconds

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "active": active,
            }
        )
        if rule_id is not UNSET:
            field_dict["rule_id"] = rule_id
        if started_at is not UNSET:
            field_dict["started_at"] = started_at
        if expires_at is not UNSET:
            field_dict["expires_at"] = expires_at
        if difficulty is not UNSET:
            field_dict["difficulty"] = difficulty
        if pass_ttl_seconds is not UNSET:
            field_dict["pass_ttl_seconds"] = pass_ttl_seconds

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        active = d.pop("active")

        rule_id = d.pop("rule_id", UNSET)

        _started_at = d.pop("started_at", UNSET)
        started_at: datetime.datetime | Unset
        if isinstance(_started_at, Unset):
            started_at = UNSET
        else:
            started_at = datetime.datetime.fromisoformat(_started_at)

        _expires_at = d.pop("expires_at", UNSET)
        expires_at: datetime.datetime | Unset
        if isinstance(_expires_at, Unset):
            expires_at = UNSET
        else:
            expires_at = datetime.datetime.fromisoformat(_expires_at)

        difficulty = d.pop("difficulty", UNSET)

        pass_ttl_seconds = d.pop("pass_ttl_seconds", UNSET)

        edge_attack_mode_status = cls(
            active=active,
            rule_id=rule_id,
            started_at=started_at,
            expires_at=expires_at,
            difficulty=difficulty,
            pass_ttl_seconds=pass_ttl_seconds,
        )

        edge_attack_mode_status.additional_properties = d
        return edge_attack_mode_status

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
