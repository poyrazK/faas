from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.flag_lifecycle_attention_item import FlagLifecycleAttentionItem, check_flag_lifecycle_attention_item
from ..models.flag_lifecycle_evidence import FlagLifecycleEvidence, check_flag_lifecycle_evidence
from ..types import UNSET, Unset

T = TypeVar("T", bound="FlagLifecycle")


@_attrs_define
class FlagLifecycle:
    """One flag's owner, expiry, last change and recent use."""

    key: str
    last_changed_version: int
    """Newest version that changed this flag, within the last 200 versions."""
    last_changed_at: datetime.datetime
    serves_one_value: bool
    """True when every request gets the same value (disabled, killed, no rules, or a first rule matching
    everybody)."""
    evidence: FlagLifecycleEvidence
    attention: list[FlagLifecycleAttentionItem]
    owner: str | Unset = UNSET
    expires_at: datetime.date | Unset = UNSET
    evaluated_requests: int | Unset = UNSET
    """Requests with a decision for this flag in the last 7 days of retained telemetry."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        key = self.key

        last_changed_version = self.last_changed_version

        last_changed_at = self.last_changed_at.isoformat()

        serves_one_value = self.serves_one_value

        evidence: str = self.evidence

        attention = []
        for attention_item_data in self.attention:
            attention_item: str = attention_item_data
            attention.append(attention_item)

        owner = self.owner

        expires_at: str | Unset = UNSET
        if not isinstance(self.expires_at, Unset):
            expires_at = self.expires_at.isoformat()

        evaluated_requests = self.evaluated_requests

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "key": key,
                "last_changed_version": last_changed_version,
                "last_changed_at": last_changed_at,
                "serves_one_value": serves_one_value,
                "evidence": evidence,
                "attention": attention,
            }
        )
        if owner is not UNSET:
            field_dict["owner"] = owner
        if expires_at is not UNSET:
            field_dict["expires_at"] = expires_at
        if evaluated_requests is not UNSET:
            field_dict["evaluated_requests"] = evaluated_requests

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        key = d.pop("key")

        last_changed_version = d.pop("last_changed_version")

        last_changed_at = datetime.datetime.fromisoformat(d.pop("last_changed_at"))

        serves_one_value = d.pop("serves_one_value")

        evidence = check_flag_lifecycle_evidence(d.pop("evidence"))

        attention = []
        _attention = d.pop("attention")
        for attention_item_data in _attention:
            attention_item = check_flag_lifecycle_attention_item(attention_item_data)

            attention.append(attention_item)

        owner = d.pop("owner", UNSET)

        _expires_at = d.pop("expires_at", UNSET)
        expires_at: datetime.date | Unset
        if isinstance(_expires_at, Unset):
            expires_at = UNSET
        else:
            expires_at = datetime.date.fromisoformat(_expires_at)

        evaluated_requests = d.pop("evaluated_requests", UNSET)

        flag_lifecycle = cls(
            key=key,
            last_changed_version=last_changed_version,
            last_changed_at=last_changed_at,
            serves_one_value=serves_one_value,
            evidence=evidence,
            attention=attention,
            owner=owner,
            expires_at=expires_at,
            evaluated_requests=evaluated_requests,
        )

        flag_lifecycle.additional_properties = d
        return flag_lifecycle

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
