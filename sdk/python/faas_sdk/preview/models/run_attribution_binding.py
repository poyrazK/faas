from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.run_binding_kind import RunBindingKind, check_run_binding_kind
from ..types import UNSET, Unset

T = TypeVar("T", bound="RunAttributionBinding")


@_attrs_define
class RunAttributionBinding:
    """Saved attribution binding and its current revision."""

    enabled: bool
    subject_kind: RunBindingKind
    """Credential or Agent endpoint type used for Run attribution."""
    subject_id: UUID
    customer_ref: str
    revision: int
    agent_ref: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        enabled = self.enabled

        subject_kind: str = self.subject_kind

        subject_id = str(self.subject_id)

        customer_ref = self.customer_ref

        revision = self.revision

        agent_ref = self.agent_ref

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "enabled": enabled,
                "subject_kind": subject_kind,
                "subject_id": subject_id,
                "customer_ref": customer_ref,
                "revision": revision,
            }
        )
        if agent_ref is not UNSET:
            field_dict["agent_ref"] = agent_ref

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        enabled = d.pop("enabled")

        subject_kind = check_run_binding_kind(d.pop("subject_kind"))

        subject_id = UUID(d.pop("subject_id"))

        customer_ref = d.pop("customer_ref")

        revision = d.pop("revision")

        agent_ref = d.pop("agent_ref", UNSET)

        run_attribution_binding = cls(
            enabled=enabled,
            subject_kind=subject_kind,
            subject_id=subject_id,
            customer_ref=customer_ref,
            revision=revision,
            agent_ref=agent_ref,
        )

        run_attribution_binding.additional_properties = d
        return run_attribution_binding

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
