from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.patch_org_request_plan import PatchOrgRequestPlan, check_patch_org_request_plan
from ..types import UNSET, Unset

T = TypeVar("T", bound="PatchOrgRequest")


@_attrs_define
class PatchOrgRequest:
    """PATCH /v1/orgs/{slug} body. Both fields are pointer-typed
    so the handler distinguishes "omitted" (leave alone) from
    "zero" (clear/empty). Authz routing:
      - name → org.manage_billing (owner + billing roles)
      - plan → org.change_plan (owner only)

    """

    name: None | str | Unset = UNSET
    plan: PatchOrgRequestPlan | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        name: None | str | Unset
        if isinstance(self.name, Unset):
            name = UNSET
        else:
            name = self.name

        plan: str | Unset = UNSET
        if not isinstance(self.plan, Unset):
            plan = self.plan

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if name is not UNSET:
            field_dict["name"] = name
        if plan is not UNSET:
            field_dict["plan"] = plan

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)

        def _parse_name(data: object) -> None | str | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(None | str | Unset, data)

        name = _parse_name(d.pop("name", UNSET))

        _plan = d.pop("plan", UNSET)
        plan: PatchOrgRequestPlan | Unset
        if isinstance(_plan, Unset):
            plan = UNSET
        else:
            plan = check_patch_org_request_plan(_plan)

        patch_org_request = cls(
            name=name,
            plan=plan,
        )

        patch_org_request.additional_properties = d
        return patch_org_request

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
