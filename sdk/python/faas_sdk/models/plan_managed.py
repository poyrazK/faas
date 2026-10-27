from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.plan_managed_not_provisioned_reason import (
    PlanManagedNotProvisionedReason,
    check_plan_managed_not_provisioned_reason,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.plan_managed_provision import PlanManagedProvision


T = TypeVar("T", bound="PlanManaged")


@_attrs_define
class PlanManaged:
    """A service the repo declared (compose image:, render.yaml, etc.)
    but the platform will NOT provision. Surfaced so the customer
    sees them in the plan and reads a learnable env hint instead of
    a runtime 422 (two-drive FROM-base contract).

    """

    name: str
    kind: str
    env_hint: str
    source: str
    image: str
    provision: PlanManagedProvision | Unset = UNSET
    """The managed PostgreSQL database a project apply creates or reuses for a Compose postgres service, and the
    service keys it binds."""
    not_provisioned_reason: PlanManagedNotProvisionedReason | Unset = UNSET
    """Why apply does not provide a Compose postgres service. Omitted for other datastores and when provision is
    set."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        name = self.name

        kind = self.kind

        env_hint = self.env_hint

        source = self.source

        image = self.image

        provision: dict[str, Any] | Unset = UNSET
        if not isinstance(self.provision, Unset):
            provision = self.provision.to_dict()

        not_provisioned_reason: str | Unset = UNSET
        if not isinstance(self.not_provisioned_reason, Unset):
            not_provisioned_reason = self.not_provisioned_reason

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "name": name,
                "kind": kind,
                "env_hint": env_hint,
                "source": source,
                "image": image,
            }
        )
        if provision is not UNSET:
            field_dict["provision"] = provision
        if not_provisioned_reason is not UNSET:
            field_dict["not_provisioned_reason"] = not_provisioned_reason

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.plan_managed_provision import PlanManagedProvision

        d = dict(src_dict)
        name = d.pop("name")

        kind = d.pop("kind")

        env_hint = d.pop("env_hint")

        source = d.pop("source")

        image = d.pop("image")

        _provision = d.pop("provision", UNSET)
        provision: PlanManagedProvision | Unset
        if isinstance(_provision, Unset):
            provision = UNSET
        else:
            provision = PlanManagedProvision.from_dict(_provision)

        _not_provisioned_reason = d.pop("not_provisioned_reason", UNSET)
        not_provisioned_reason: PlanManagedNotProvisionedReason | Unset
        if isinstance(_not_provisioned_reason, Unset):
            not_provisioned_reason = UNSET
        else:
            not_provisioned_reason = check_plan_managed_not_provisioned_reason(_not_provisioned_reason)

        plan_managed = cls(
            name=name,
            kind=kind,
            env_hint=env_hint,
            source=source,
            image=image,
            provision=provision,
            not_provisioned_reason=not_provisioned_reason,
        )

        plan_managed.additional_properties = d
        return plan_managed

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
