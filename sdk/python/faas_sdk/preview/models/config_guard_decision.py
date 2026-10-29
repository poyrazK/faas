from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="ConfigGuardDecision")


@_attrs_define
class ConfigGuardDecision:
    """Why a config guard closed: the rule, a one-line summary and the new-config and old-config samples."""

    rule: str
    summary: str
    new_requests: int
    new_server_errors: int
    old_requests: int
    old_server_errors: int
    restored_revision: int | Unset = UNSET
    not_restorable: list[str] | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        rule = self.rule

        summary = self.summary

        new_requests = self.new_requests

        new_server_errors = self.new_server_errors

        old_requests = self.old_requests

        old_server_errors = self.old_server_errors

        restored_revision = self.restored_revision

        not_restorable: list[str] | Unset = UNSET
        if not isinstance(self.not_restorable, Unset):
            not_restorable = self.not_restorable

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "rule": rule,
                "summary": summary,
                "new_requests": new_requests,
                "new_server_errors": new_server_errors,
                "old_requests": old_requests,
                "old_server_errors": old_server_errors,
            }
        )
        if restored_revision is not UNSET:
            field_dict["restored_revision"] = restored_revision
        if not_restorable is not UNSET:
            field_dict["not_restorable"] = not_restorable

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        rule = d.pop("rule")

        summary = d.pop("summary")

        new_requests = d.pop("new_requests")

        new_server_errors = d.pop("new_server_errors")

        old_requests = d.pop("old_requests")

        old_server_errors = d.pop("old_server_errors")

        restored_revision = d.pop("restored_revision", UNSET)

        not_restorable = cast(list[str], d.pop("not_restorable", UNSET))

        config_guard_decision = cls(
            rule=rule,
            summary=summary,
            new_requests=new_requests,
            new_server_errors=new_server_errors,
            old_requests=old_requests,
            old_server_errors=old_server_errors,
            restored_revision=restored_revision,
            not_restorable=not_restorable,
        )

        config_guard_decision.additional_properties = d
        return config_guard_decision

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
