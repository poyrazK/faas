from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.password_login_response_plan import PasswordLoginResponsePlan, check_password_login_response_plan

T = TypeVar("T", bound="PasswordLoginResponse")


@_attrs_define
class PasswordLoginResponse:
    """Successful sign-in. The session cookie is set via
    `Set-Cookie: faas_sid=…`; the body carries only
    `{account_id, plan}`. No API key is ever returned on
    login (issue #165, PR #2).

    """

    account_id: str
    plan: PasswordLoginResponsePlan
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        account_id = self.account_id

        plan: str = self.plan

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "account_id": account_id,
                "plan": plan,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        account_id = d.pop("account_id")

        plan = check_password_login_response_plan(d.pop("plan"))

        password_login_response = cls(
            account_id=account_id,
            plan=plan,
        )

        password_login_response.additional_properties = d
        return password_login_response

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
