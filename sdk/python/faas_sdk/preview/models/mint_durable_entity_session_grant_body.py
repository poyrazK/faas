from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="MintDurableEntitySessionGrantBody")


@_attrs_define
class MintDurableEntitySessionGrantBody:
    """Mint durable entity session grant — requestBody / content / application/json / schema: Preview mutation input with
    server-side size, scope and revision validation.

    """

    namespace: str
    """Mint durable entity session grant — requestBody / content / application/json / schema / properties /
    namespace: Exact entity namespace."""
    key: str
    """Mint durable entity session grant — requestBody / content / application/json / schema / properties / key:
    Exact logical entity key."""
    session_id: str
    """Mint durable entity session grant — requestBody / content / application/json / schema / properties / session
    id: Existing logical session identity."""
    generation: int
    """Mint durable entity session grant — requestBody / content / application/json / schema / properties /
    generation: Current session generation."""
    origin: str
    """Mint durable entity session grant — requestBody / content / application/json / schema / properties / origin:
    Exact browser origin without a path, query or fragment."""
    commands: list[str]
    """Mint durable entity session grant — requestBody / content / application/json / schema / properties /
    commands: Allowed command names; delegation does not grant other commands."""
    ttl_seconds: int
    """Mint durable entity session grant — requestBody / content / application/json / schema / properties / ttl
    seconds: Credential lifetime in seconds."""
    env: str | Unset = UNSET
    """Mint durable entity session grant — requestBody / content / application/json / schema / properties / env:
    Registered environment; defaults to production."""
    platform_tenant_id: str | Unset = UNSET
    """Mint durable entity session grant — requestBody / content / application/json / schema / properties /
    platform tenant id: Optional same-account platform tenant."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        namespace = self.namespace

        key = self.key

        session_id = self.session_id

        generation = self.generation

        origin = self.origin

        commands = self.commands

        ttl_seconds = self.ttl_seconds

        env = self.env

        platform_tenant_id = self.platform_tenant_id

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "namespace": namespace,
                "key": key,
                "session_id": session_id,
                "generation": generation,
                "origin": origin,
                "commands": commands,
                "ttl_seconds": ttl_seconds,
            }
        )
        if env is not UNSET:
            field_dict["env"] = env
        if platform_tenant_id is not UNSET:
            field_dict["platform_tenant_id"] = platform_tenant_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        namespace = d.pop("namespace")

        key = d.pop("key")

        session_id = d.pop("session_id")

        generation = d.pop("generation")

        origin = d.pop("origin")

        commands = cast(list[str], d.pop("commands"))

        ttl_seconds = d.pop("ttl_seconds")

        env = d.pop("env", UNSET)

        platform_tenant_id = d.pop("platform_tenant_id", UNSET)

        mint_durable_entity_session_grant_body = cls(
            namespace=namespace,
            key=key,
            session_id=session_id,
            generation=generation,
            origin=origin,
            commands=commands,
            ttl_seconds=ttl_seconds,
            env=env,
            platform_tenant_id=platform_tenant_id,
        )

        mint_durable_entity_session_grant_body.additional_properties = d
        return mint_durable_entity_session_grant_body

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
