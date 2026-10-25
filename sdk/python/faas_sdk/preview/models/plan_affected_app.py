from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.plan_affected_app_action import PlanAffectedAppAction, check_plan_affected_app_action
from ..types import UNSET, Unset

T = TypeVar("T", bound="PlanAffectedApp")


@_attrs_define
class PlanAffectedApp:
    """One row of the ADR-124 blast-radius partition. action is closed-
    vocabulary:
      create — scan workload, no existing app row matches (root_dir, name).
      update — scan workload, existing app matches.
      remove — existing app, no scan workload, not protected by --exclude.
      noop   — operator excluded via --exclude, or no scan change.
    id is empty for action == create. existing_root_dir is populated
    only when the existing app's root_dir differs from the scan root_dir
    (monorepo collision surface).

    """

    slug: str
    action: PlanAffectedAppAction
    id: str | Unset = UNSET
    existing_root_dir: str | Unset = UNSET
    """RootDir of the existing app row. Empty for create. Populated only when it differs from the scan-time
    RootDir."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        slug = self.slug

        action: str = self.action

        id = self.id

        existing_root_dir = self.existing_root_dir

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "slug": slug,
                "action": action,
            }
        )
        if id is not UNSET:
            field_dict["id"] = id
        if existing_root_dir is not UNSET:
            field_dict["existing_root_dir"] = existing_root_dir

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        slug = d.pop("slug")

        action = check_plan_affected_app_action(d.pop("action"))

        id = d.pop("id", UNSET)

        existing_root_dir = d.pop("existing_root_dir", UNSET)

        plan_affected_app = cls(
            slug=slug,
            action=action,
            id=id,
            existing_root_dir=existing_root_dir,
        )

        plan_affected_app.additional_properties = d
        return plan_affected_app

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
