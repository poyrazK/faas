from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.app_response import AppResponse
    from ..models.dev_bucket_response import DevBucketResponse
    from ..models.dev_postgres_response import DevPostgresResponse
    from ..models.dev_watch import DevWatch


T = TypeVar("T", bound="DevSessionResponse")


@_attrs_define
class DevSessionResponse:
    """Stable remote developer workspace and its renewable lease."""

    app: AppResponse
    """An app: slug, type, runtime (for functions), RAM/cpu/idle-timeout config, current state, last-deploy
    pointer, per-app outbound CIDR allowlist (ADR-031 + ADR-032), and reactive scale-up trigger targets (issue #169
    / #172)."""
    expires_at: datetime.datetime
    postgres: DevPostgresResponse | Unset = UNSET
    """Safe receipt for the isolated developer database and its app binding. Credentials are injected into
    DATABASE_URL and never returned."""
    buckets: list[DevBucketResponse] | Unset = UNSET
    """Active automatic bucket bindings, including resources omitted from the latest upsert. Included only for
    principals with storage:manage or admin. Revoked bindings are excluded."""
    watch: DevWatch | Unset = UNSET
    """Watch mode (ADR-1170). The environment runs this development server instead of the production build, and
    live source patches reach the server's own reloader. Omit to turn watch mode off; the change applies from the
    next developer build. Refused with 422 dev_watch_unsupported when the developer app has less than 512 MB of RAM
    or does not require authentication."""
    go_command: str | Unset = UNSET
    """The stored Go command (ADR-20261026120412); omitted when none."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        app = self.app.to_dict()

        expires_at = self.expires_at.isoformat()

        postgres: dict[str, Any] | Unset = UNSET
        if not isinstance(self.postgres, Unset):
            postgres = self.postgres.to_dict()

        buckets: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.buckets, Unset):
            buckets = []
            for buckets_item_data in self.buckets:
                buckets_item = buckets_item_data.to_dict()
                buckets.append(buckets_item)

        watch: dict[str, Any] | Unset = UNSET
        if not isinstance(self.watch, Unset):
            watch = self.watch.to_dict()

        go_command = self.go_command

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "app": app,
                "expires_at": expires_at,
            }
        )
        if postgres is not UNSET:
            field_dict["postgres"] = postgres
        if buckets is not UNSET:
            field_dict["buckets"] = buckets
        if watch is not UNSET:
            field_dict["watch"] = watch
        if go_command is not UNSET:
            field_dict["go_command"] = go_command

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.app_response import AppResponse
        from ..models.dev_bucket_response import DevBucketResponse
        from ..models.dev_postgres_response import DevPostgresResponse
        from ..models.dev_watch import DevWatch

        d = dict(src_dict)
        app = AppResponse.from_dict(d.pop("app"))

        expires_at = datetime.datetime.fromisoformat(d.pop("expires_at"))

        _postgres = d.pop("postgres", UNSET)
        postgres: DevPostgresResponse | Unset
        if isinstance(_postgres, Unset):
            postgres = UNSET
        else:
            postgres = DevPostgresResponse.from_dict(_postgres)

        _buckets = d.pop("buckets", UNSET)
        buckets: list[DevBucketResponse] | Unset = UNSET
        if _buckets is not UNSET:
            buckets = []
            for buckets_item_data in _buckets:
                buckets_item = DevBucketResponse.from_dict(buckets_item_data)

                buckets.append(buckets_item)

        _watch = d.pop("watch", UNSET)
        watch: DevWatch | Unset
        if isinstance(_watch, Unset):
            watch = UNSET
        else:
            watch = DevWatch.from_dict(_watch)

        go_command = d.pop("go_command", UNSET)

        dev_session_response = cls(
            app=app,
            expires_at=expires_at,
            postgres=postgres,
            buckets=buckets,
            watch=watch,
            go_command=go_command,
        )

        dev_session_response.additional_properties = d
        return dev_session_response

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
