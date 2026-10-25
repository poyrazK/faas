from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.app_secret_list_response_secrets_by_scope import AppSecretListResponseSecretsByScope
    from ..models.app_secret_response import AppSecretResponse


T = TypeVar("T", bound="AppSecretListResponse")


@_attrs_define
class AppSecretListResponse:
    """Paginated list of sealed-secret envelopes (no plaintext). Discriminated union: `secrets` is the flat arm;
    `secrets_by_scope` is the `?scope=__all__` arm (ADR-092 PR-B).

    """

    secrets: list[AppSecretResponse]
    quota_max: int
    count: int
    secrets_by_scope: AppSecretListResponseSecretsByScope | Unset = UNSET
    """Nested per-scope map (ADR-092 PR-B, mirror of ADR-090 PR-B D3). Populated only when `?scope=__all__` is
    passed; keys are scope names, values are per-scope row lists ordered by key ASC."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        secrets = []
        for secrets_item_data in self.secrets:
            secrets_item = secrets_item_data.to_dict()
            secrets.append(secrets_item)

        quota_max = self.quota_max

        count = self.count

        secrets_by_scope: dict[str, Any] | Unset = UNSET
        if not isinstance(self.secrets_by_scope, Unset):
            secrets_by_scope = self.secrets_by_scope.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "secrets": secrets,
                "quota_max": quota_max,
                "count": count,
            }
        )
        if secrets_by_scope is not UNSET:
            field_dict["secrets_by_scope"] = secrets_by_scope

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.app_secret_list_response_secrets_by_scope import AppSecretListResponseSecretsByScope
        from ..models.app_secret_response import AppSecretResponse

        d = dict(src_dict)
        secrets = []
        _secrets = d.pop("secrets")
        for secrets_item_data in _secrets:
            secrets_item = AppSecretResponse.from_dict(secrets_item_data)

            secrets.append(secrets_item)

        quota_max = d.pop("quota_max")

        count = d.pop("count")

        _secrets_by_scope = d.pop("secrets_by_scope", UNSET)
        secrets_by_scope: AppSecretListResponseSecretsByScope | Unset
        if isinstance(_secrets_by_scope, Unset):
            secrets_by_scope = UNSET
        else:
            secrets_by_scope = AppSecretListResponseSecretsByScope.from_dict(_secrets_by_scope)

        app_secret_list_response = cls(
            secrets=secrets,
            quota_max=quota_max,
            count=count,
            secrets_by_scope=secrets_by_scope,
        )

        app_secret_list_response.additional_properties = d
        return app_secret_list_response

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
