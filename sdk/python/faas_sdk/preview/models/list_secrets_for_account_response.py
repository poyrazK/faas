from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.account_app_secret_response import AccountAppSecretResponse


T = TypeVar("T", bound="ListSecretsForAccountResponse")


@_attrs_define
class ListSecretsForAccountResponse:
    """Page shape for `GET /v1/secrets` (issue #393). `secrets` is
    the page in (app_slug ASC, key ASC) order. `next_before` is
    the cursor the caller passes on the next request — encoded as
    `<slug>|<key>`. Empty / null at the end.

    """

    secrets: list[AccountAppSecretResponse]
    next_before: None | str | Unset = UNSET
    """Cursor in the form `<slug>|<key>`. Empty / null at the end."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        secrets = []
        for secrets_item_data in self.secrets:
            secrets_item = secrets_item_data.to_dict()
            secrets.append(secrets_item)

        next_before: None | str | Unset
        if isinstance(self.next_before, Unset):
            next_before = UNSET
        else:
            next_before = self.next_before

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "secrets": secrets,
            }
        )
        if next_before is not UNSET:
            field_dict["next_before"] = next_before

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.account_app_secret_response import AccountAppSecretResponse

        d = dict(src_dict)
        secrets = []
        _secrets = d.pop("secrets")
        for secrets_item_data in _secrets:
            secrets_item = AccountAppSecretResponse.from_dict(secrets_item_data)

            secrets.append(secrets_item)

        def _parse_next_before(data: object) -> None | str | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(None | str | Unset, data)

        next_before = _parse_next_before(d.pop("next_before", UNSET))

        list_secrets_for_account_response = cls(
            secrets=secrets,
            next_before=next_before,
        )

        list_secrets_for_account_response.additional_properties = d
        return list_secrets_for_account_response

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
