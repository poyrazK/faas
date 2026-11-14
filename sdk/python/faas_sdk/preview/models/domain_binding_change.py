from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.domain_binding_change_action import DomainBindingChangeAction, check_domain_binding_change_action
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.domain_binding_target import DomainBindingTarget


T = TypeVar("T", bound="DomainBindingChange")


@_attrs_define
class DomainBindingChange:
    """A proposed create, move or noop. New claims need verification. A move retains proof and TLS state."""

    domain: str | Unset = UNSET
    action: DomainBindingChangeAction | Unset = UNSET
    before: DomainBindingTarget | Unset = UNSET
    """Resolved app and environment identity captured in a reviewed plan."""
    after: DomainBindingTarget | Unset = UNSET
    """Resolved app and environment identity captured in a reviewed plan."""
    verified: bool | Unset = UNSET
    clears_default: bool | Unset = UNSET
    """The source app's default domain pointer will be cleared."""
    deployment_ids: list[str] | Unset = UNSET
    """Serving destination deployments captured for review; this is not an HTTP smoke-test result."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        domain = self.domain

        action: str | Unset = UNSET
        if not isinstance(self.action, Unset):
            action = self.action

        before: dict[str, Any] | Unset = UNSET
        if not isinstance(self.before, Unset):
            before = self.before.to_dict()

        after: dict[str, Any] | Unset = UNSET
        if not isinstance(self.after, Unset):
            after = self.after.to_dict()

        verified = self.verified

        clears_default = self.clears_default

        deployment_ids: list[str] | Unset = UNSET
        if not isinstance(self.deployment_ids, Unset):
            deployment_ids = self.deployment_ids

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if domain is not UNSET:
            field_dict["domain"] = domain
        if action is not UNSET:
            field_dict["action"] = action
        if before is not UNSET:
            field_dict["before"] = before
        if after is not UNSET:
            field_dict["after"] = after
        if verified is not UNSET:
            field_dict["verified"] = verified
        if clears_default is not UNSET:
            field_dict["clears_default"] = clears_default
        if deployment_ids is not UNSET:
            field_dict["deployment_ids"] = deployment_ids

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.domain_binding_target import DomainBindingTarget

        d = dict(src_dict)
        domain = d.pop("domain", UNSET)

        _action = d.pop("action", UNSET)
        action: DomainBindingChangeAction | Unset
        if isinstance(_action, Unset):
            action = UNSET
        else:
            action = check_domain_binding_change_action(_action)

        _before = d.pop("before", UNSET)
        before: DomainBindingTarget | Unset
        if isinstance(_before, Unset):
            before = UNSET
        else:
            before = DomainBindingTarget.from_dict(_before)

        _after = d.pop("after", UNSET)
        after: DomainBindingTarget | Unset
        if isinstance(_after, Unset):
            after = UNSET
        else:
            after = DomainBindingTarget.from_dict(_after)

        verified = d.pop("verified", UNSET)

        clears_default = d.pop("clears_default", UNSET)

        deployment_ids = cast(list[str], d.pop("deployment_ids", UNSET))

        domain_binding_change = cls(
            domain=domain,
            action=action,
            before=before,
            after=after,
            verified=verified,
            clears_default=clears_default,
            deployment_ids=deployment_ids,
        )

        domain_binding_change.additional_properties = d
        return domain_binding_change

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
