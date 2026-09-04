from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.account_response_plan import AccountResponsePlan, check_account_response_plan
from ..models.account_response_requested_plan import AccountResponseRequestedPlan, check_account_response_requested_plan
from ..models.account_response_status import AccountResponseStatus, check_account_response_status
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.account_limits import AccountLimits


T = TypeVar("T", bound="AccountResponse")


@_attrs_define
class AccountResponse:
    """Account profile: id, email, plan, status, limits snapshot, current-month usage, and total app count."""

    id: str
    email: str
    plan: AccountResponsePlan
    status: AccountResponseStatus
    limits: AccountLimits
    """Plan-driven quota and resource caps: max RAM per app, concurrent wakes, total deployed apps, included GB-
    hours, and max app-layer bytes per build."""
    usage_gb_hours: float
    app_count: int
    github_install_id: None | str | Unset = UNSET
    plan_change_status: str | Unset = UNSET
    requested_plan: AccountResponseRequestedPlan | Unset = UNSET
    effective_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = self.id

        email = self.email

        plan: str = self.plan

        status: str = self.status

        limits = self.limits.to_dict()

        usage_gb_hours = self.usage_gb_hours

        app_count = self.app_count

        github_install_id: None | str | Unset
        if isinstance(self.github_install_id, Unset):
            github_install_id = UNSET
        else:
            github_install_id = self.github_install_id

        plan_change_status = self.plan_change_status

        requested_plan: str | Unset = UNSET
        if not isinstance(self.requested_plan, Unset):
            requested_plan = self.requested_plan

        effective_at: str | Unset = UNSET
        if not isinstance(self.effective_at, Unset):
            effective_at = self.effective_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "email": email,
                "plan": plan,
                "status": status,
                "limits": limits,
                "usage_gb_hours": usage_gb_hours,
                "app_count": app_count,
            }
        )
        if github_install_id is not UNSET:
            field_dict["github_install_id"] = github_install_id
        if plan_change_status is not UNSET:
            field_dict["plan_change_status"] = plan_change_status
        if requested_plan is not UNSET:
            field_dict["requested_plan"] = requested_plan
        if effective_at is not UNSET:
            field_dict["effective_at"] = effective_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.account_limits import AccountLimits

        d = dict(src_dict)
        id = d.pop("id")

        email = d.pop("email")

        plan = check_account_response_plan(d.pop("plan"))

        status = check_account_response_status(d.pop("status"))

        limits = AccountLimits.from_dict(d.pop("limits"))

        usage_gb_hours = d.pop("usage_gb_hours")

        app_count = d.pop("app_count")

        def _parse_github_install_id(data: object) -> None | str | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(None | str | Unset, data)

        github_install_id = _parse_github_install_id(d.pop("github_install_id", UNSET))

        plan_change_status = d.pop("plan_change_status", UNSET)

        _requested_plan = d.pop("requested_plan", UNSET)
        requested_plan: AccountResponseRequestedPlan | Unset
        if isinstance(_requested_plan, Unset):
            requested_plan = UNSET
        else:
            requested_plan = check_account_response_requested_plan(_requested_plan)

        _effective_at = d.pop("effective_at", UNSET)
        effective_at: datetime.datetime | Unset
        if isinstance(_effective_at, Unset):
            effective_at = UNSET
        else:
            effective_at = datetime.datetime.fromisoformat(_effective_at)

        account_response = cls(
            id=id,
            email=email,
            plan=plan,
            status=status,
            limits=limits,
            usage_gb_hours=usage_gb_hours,
            app_count=app_count,
            github_install_id=github_install_id,
            plan_change_status=plan_change_status,
            requested_plan=requested_plan,
            effective_at=effective_at,
        )

        account_response.additional_properties = d
        return account_response

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
