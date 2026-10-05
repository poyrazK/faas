from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.app_env_list_response_env_by_scope import AppEnvListResponseEnvByScope
    from ..models.app_env_response import AppEnvResponse


T = TypeVar("T", bound="AppEnvListResponse")


@_attrs_define
class AppEnvListResponse:
    """List of env var envelopes (no plaintext values). Discriminated
    union (ADR-090 PR-B D3):

      * `env` array populated, `env_by_scope` omitted: the
        default-scope or per-scope read (omitted or `?scope=<slug>`).
        `count` is the per-scope row count.
      * `env` empty, `env_by_scope` populated: the `?scope=__all__`
        read. `count` is the cross-scope row total.

    Both arms are valid wire shapes for a GET; the `?scope=`
    query discriminates.

    """

    env: list[AppEnvResponse]
    quota_max: int
    count: int
    env_by_scope: AppEnvListResponseEnvByScope | Unset = UNSET
    """Nested per-scope map (ADR-090 PR-B D3). Populated only when `?scope=__all__` is passed; keys are scope
    names, values are per-scope row lists ordered by key ASC."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        env = []
        for env_item_data in self.env:
            env_item = env_item_data.to_dict()
            env.append(env_item)

        quota_max = self.quota_max

        count = self.count

        env_by_scope: dict[str, Any] | Unset = UNSET
        if not isinstance(self.env_by_scope, Unset):
            env_by_scope = self.env_by_scope.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "env": env,
                "quota_max": quota_max,
                "count": count,
            }
        )
        if env_by_scope is not UNSET:
            field_dict["env_by_scope"] = env_by_scope

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.app_env_list_response_env_by_scope import AppEnvListResponseEnvByScope
        from ..models.app_env_response import AppEnvResponse

        d = dict(src_dict)
        env = []
        _env = d.pop("env")
        for env_item_data in _env:
            env_item = AppEnvResponse.from_dict(env_item_data)

            env.append(env_item)

        quota_max = d.pop("quota_max")

        count = d.pop("count")

        _env_by_scope = d.pop("env_by_scope", UNSET)
        env_by_scope: AppEnvListResponseEnvByScope | Unset
        if isinstance(_env_by_scope, Unset):
            env_by_scope = UNSET
        else:
            env_by_scope = AppEnvListResponseEnvByScope.from_dict(_env_by_scope)

        app_env_list_response = cls(
            env=env,
            quota_max=quota_max,
            count=count,
            env_by_scope=env_by_scope,
        )

        app_env_list_response.additional_properties = d
        return app_env_list_response

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
