from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.custom_domain_response import CustomDomainResponse
    from ..models.domain_manifest import DomainManifest
    from ..models.domain_plan import DomainPlan


T = TypeVar("T", bound="DomainApplyResponse")


@_attrs_define
class DomainApplyResponse:
    """The committed snapshot and optional previous bindings for a separately reviewed rollback."""

    plan: DomainPlan | Unset = UNSET
    """Deterministic review artifact. The SHA-256 binds the diff to private claim identity, intervening moves,
    destination release and traffic allocations. It contains no ownership proof token."""
    domains: list[CustomDomainResponse] | Unset = UNSET
    rollback: DomainManifest | Unset = UNSET
    """Versioned desired bindings. Only listed domains change; omission never removes a binding. At most 20
    bindings in a 128 KiB request. Use a separate file from the application deployment manifest."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        plan: dict[str, Any] | Unset = UNSET
        if not isinstance(self.plan, Unset):
            plan = self.plan.to_dict()

        domains: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.domains, Unset):
            domains = []
            for domains_item_data in self.domains:
                domains_item = domains_item_data.to_dict()
                domains.append(domains_item)

        rollback: dict[str, Any] | Unset = UNSET
        if not isinstance(self.rollback, Unset):
            rollback = self.rollback.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if plan is not UNSET:
            field_dict["plan"] = plan
        if domains is not UNSET:
            field_dict["domains"] = domains
        if rollback is not UNSET:
            field_dict["rollback"] = rollback

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.custom_domain_response import CustomDomainResponse
        from ..models.domain_manifest import DomainManifest
        from ..models.domain_plan import DomainPlan

        d = dict(src_dict)
        _plan = d.pop("plan", UNSET)
        plan: DomainPlan | Unset
        if isinstance(_plan, Unset):
            plan = UNSET
        else:
            plan = DomainPlan.from_dict(_plan)

        _domains = d.pop("domains", UNSET)
        domains: list[CustomDomainResponse] | Unset = UNSET
        if _domains is not UNSET:
            domains = []
            for domains_item_data in _domains:
                domains_item = CustomDomainResponse.from_dict(domains_item_data)

                domains.append(domains_item)

        _rollback = d.pop("rollback", UNSET)
        rollback: DomainManifest | Unset
        if isinstance(_rollback, Unset):
            rollback = UNSET
        else:
            rollback = DomainManifest.from_dict(_rollback)

        domain_apply_response = cls(
            plan=plan,
            domains=domains,
            rollback=rollback,
        )

        domain_apply_response.additional_properties = d
        return domain_apply_response

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
