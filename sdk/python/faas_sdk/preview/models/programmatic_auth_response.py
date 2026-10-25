from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.programmatic_auth_response_plan import ProgrammaticAuthResponsePlan, check_programmatic_auth_response_plan

if TYPE_CHECKING:
    from ..models.programmatic_api_key import ProgrammaticAPIKey


T = TypeVar("T", bound="ProgrammaticAuthResponse")


@_attrs_define
class ProgrammaticAuthResponse:
    """Body for the JSON-only POST /v1/auth/{signup,login} pair
    (issue #311). Distinct from PasswordLoginResponse: this
    one carries the `api_key` payload so the bearer-key CLI
    can use the result without a dashboard round-trip. The
    plaintext is returned ONCE; the caller persists it via
    `saveToken()` before this response is dropped.

    Email is echoed back so the CLI's finalizeLogin step can
    render "Logged in as <email> (<plan> plan)" without an
    extra Whoami round-trip.

    """

    account_id: str
    """Account UUID."""
    email: str
    """Email the client sent in the POST body. Echoed back so the CLI can render the success line without a Whoami
    round-trip."""
    plan: ProgrammaticAuthResponsePlan
    api_key: ProgrammaticAPIKey
    """Freshly minted API key returned on the first
    request. The `plaintext` field is the only copy the
    caller will ever see — store it in `~/.config/faas/auth.json`
    immediately. The `id` is the row's UUID for later
    list/delete via `/v1/keys`.
    """
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        account_id = self.account_id

        email = self.email

        plan: str = self.plan

        api_key = self.api_key.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "account_id": account_id,
                "email": email,
                "plan": plan,
                "api_key": api_key,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.programmatic_api_key import ProgrammaticAPIKey

        d = dict(src_dict)
        account_id = d.pop("account_id")

        email = d.pop("email")

        plan = check_programmatic_auth_response_plan(d.pop("plan"))

        api_key = ProgrammaticAPIKey.from_dict(d.pop("api_key"))

        programmatic_auth_response = cls(
            account_id=account_id,
            email=email,
            plan=plan,
            api_key=api_key,
        )

        programmatic_auth_response.additional_properties = d
        return programmatic_auth_response

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
