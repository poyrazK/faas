from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define

from ..models.data_api_access_config_version import DataAPIAccessConfigVersion, check_data_api_access_config_version
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.data_api_server_key import DataAPIServerKey
    from ..models.data_api_user_auth import DataAPIUserAuth


T = TypeVar("T", bound="DataAPIAccessConfig")


@_attrs_define
class DataAPIAccessConfig:
    """Access settings the runtime reports for live reads, sent after start and hourly. It replaces the stored settings
    unless the stored ones come from a runtime process that started later; turning usage reporting off forgets them.
    Holds key hashes, never keys.

    """

    version: DataAPIAccessConfigVersion
    schemas: list[str]
    auth: DataAPIUserAuth | Unset = UNSET
    """The issuer, JWKS URL and audience the Data API verifies user tokens against. Live reads verify user tokens
    with the same settings."""
    public_read: list[str] | Unset = UNSET
    server_keys: list[DataAPIServerKey] | Unset = UNSET
    allow_without_rls: list[str] | Unset = UNSET
    """Relations signed-in users may read without row-level security; live reads apply the same exemptions."""
    started_at: datetime.datetime | Unset = UNSET
    """When the reporting runtime process started. A report never replaces settings from a process that started
    later."""

    def to_dict(self) -> dict[str, Any]:
        version: int = self.version

        schemas = self.schemas

        auth: dict[str, Any] | Unset = UNSET
        if not isinstance(self.auth, Unset):
            auth = self.auth.to_dict()

        public_read: list[str] | Unset = UNSET
        if not isinstance(self.public_read, Unset):
            public_read = self.public_read

        server_keys: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.server_keys, Unset):
            server_keys = []
            for server_keys_item_data in self.server_keys:
                server_keys_item = server_keys_item_data.to_dict()
                server_keys.append(server_keys_item)

        allow_without_rls: list[str] | Unset = UNSET
        if not isinstance(self.allow_without_rls, Unset):
            allow_without_rls = self.allow_without_rls

        started_at: str | Unset = UNSET
        if not isinstance(self.started_at, Unset):
            started_at = self.started_at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "version": version,
                "schemas": schemas,
            }
        )
        if auth is not UNSET:
            field_dict["auth"] = auth
        if public_read is not UNSET:
            field_dict["public_read"] = public_read
        if server_keys is not UNSET:
            field_dict["server_keys"] = server_keys
        if allow_without_rls is not UNSET:
            field_dict["allow_without_rls"] = allow_without_rls
        if started_at is not UNSET:
            field_dict["started_at"] = started_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.data_api_server_key import DataAPIServerKey
        from ..models.data_api_user_auth import DataAPIUserAuth

        d = dict(src_dict)
        version = check_data_api_access_config_version(d.pop("version"))

        schemas = cast(list[str], d.pop("schemas"))

        _auth = d.pop("auth", UNSET)
        auth: DataAPIUserAuth | Unset
        if isinstance(_auth, Unset):
            auth = UNSET
        else:
            auth = DataAPIUserAuth.from_dict(_auth)

        public_read = cast(list[str], d.pop("public_read", UNSET))

        _server_keys = d.pop("server_keys", UNSET)
        server_keys: list[DataAPIServerKey] | Unset = UNSET
        if _server_keys is not UNSET:
            server_keys = []
            for server_keys_item_data in _server_keys:
                server_keys_item = DataAPIServerKey.from_dict(server_keys_item_data)

                server_keys.append(server_keys_item)

        allow_without_rls = cast(list[str], d.pop("allow_without_rls", UNSET))

        _started_at = d.pop("started_at", UNSET)
        started_at: datetime.datetime | Unset
        if isinstance(_started_at, Unset):
            started_at = UNSET
        else:
            started_at = datetime.datetime.fromisoformat(_started_at)

        data_api_access_config = cls(
            version=version,
            schemas=schemas,
            auth=auth,
            public_read=public_read,
            server_keys=server_keys,
            allow_without_rls=allow_without_rls,
            started_at=started_at,
        )

        return data_api_access_config
