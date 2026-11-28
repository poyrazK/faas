from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.project_initial_secrets import ProjectInitialSecrets


T = TypeVar("T", bound="ApplyCachedProjectPlanRequest")


@_attrs_define
class ApplyCachedProjectPlanRequest:
    """Applies an account-owned reviewed project archive with selected workloads and optional initial sealed configuration."""

    plan_token: str
    only: list[str] | Unset = UNSET
    """Selected workload names; unselected existing workloads remain unchanged."""
    exclude: list[str] | Unset = UNSET
    approval_token: str | Unset = UNSET
    initial_secrets: ProjectInitialSecrets | Unset = UNSET
    """Request-only mapping of exact selected workload names to initial sealed environment values. Values stay in
    memory until sealing and never enter responses or source caches."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        plan_token = self.plan_token

        only: list[str] | Unset = UNSET
        if not isinstance(self.only, Unset):
            only = self.only

        exclude: list[str] | Unset = UNSET
        if not isinstance(self.exclude, Unset):
            exclude = self.exclude

        approval_token = self.approval_token

        initial_secrets: dict[str, Any] | Unset = UNSET
        if not isinstance(self.initial_secrets, Unset):
            initial_secrets = self.initial_secrets.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "plan_token": plan_token,
            }
        )
        if only is not UNSET:
            field_dict["only"] = only
        if exclude is not UNSET:
            field_dict["exclude"] = exclude
        if approval_token is not UNSET:
            field_dict["approval_token"] = approval_token
        if initial_secrets is not UNSET:
            field_dict["initial_secrets"] = initial_secrets

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.project_initial_secrets import ProjectInitialSecrets

        d = dict(src_dict)
        plan_token = d.pop("plan_token")

        only = cast(list[str], d.pop("only", UNSET))

        exclude = cast(list[str], d.pop("exclude", UNSET))

        approval_token = d.pop("approval_token", UNSET)

        _initial_secrets = d.pop("initial_secrets", UNSET)
        initial_secrets: ProjectInitialSecrets | Unset
        if isinstance(_initial_secrets, Unset):
            initial_secrets = UNSET
        else:
            initial_secrets = ProjectInitialSecrets.from_dict(_initial_secrets)

        apply_cached_project_plan_request = cls(
            plan_token=plan_token,
            only=only,
            exclude=exclude,
            approval_token=approval_token,
            initial_secrets=initial_secrets,
        )

        apply_cached_project_plan_request.additional_properties = d
        return apply_cached_project_plan_request

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
