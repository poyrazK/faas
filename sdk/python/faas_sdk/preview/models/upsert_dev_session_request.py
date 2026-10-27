from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.upsert_dev_session_request_runtime import (
    UpsertDevSessionRequestRuntime,
    check_upsert_dev_session_request_runtime,
)
from ..models.upsert_dev_session_request_type import UpsertDevSessionRequestType, check_upsert_dev_session_request_type
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.dev_bucket_request import DevBucketRequest
    from ..models.dev_postgres_request import DevPostgresRequest
    from ..models.dev_watch import DevWatch


T = TypeVar("T", bound="UpsertDevSessionRequest")


@_attrs_define
class UpsertDevSessionRequest:
    """Application shape and opaque local identity for a CLI-managed developer environment."""

    type_: UpsertDevSessionRequestType | Unset = "app"
    runtime: UpsertDevSessionRequestRuntime | Unset = UNSET
    workspace_id: str | Unset = UNSET
    """Opaque identity derived locally from the CLI installation and canonical source path. Omit only for legacy
    sessions."""
    postgres: DevPostgresRequest | Unset = UNSET
    """Opts the developer environment into an isolated managed PostgreSQL database."""
    buckets: list[DevBucketRequest] | Unset = UNSET
    """Explicitly provision private workspace buckets and bindings. Requires storage:manage or admin in addition to
    developer session authorization. Omission preserves existing resources. Repeating a revoked binding returns
    conflict."""
    lease_seconds: int | Unset = UNSET
    """How long the environment survives after this request. Omit for the 24-hour default. Values above the plan's
    developer lease maximum return 403 plan_limit_developer_lease."""
    watch: DevWatch | Unset = UNSET
    """Watch mode (ADR-1170). The environment runs this development server instead of the production build, and
    live source patches reach the server's own reloader. Omit to turn watch mode off; the change applies from the
    next developer build. Refused with 422 dev_watch_unsupported when the developer app has less than 512 MB of RAM
    or does not require authentication."""
    go_command: str | Unset = UNSET
    """Builds cmd/<go_command> of the Go module at the source root (ADR-20261026120412), as a project service's Go
    command does. Omit to build what the source builds by default; applies from the next developer build. Refused
    with 400 for a function or a name that is not a cmd/ directory name."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        type_: str | Unset = UNSET
        if not isinstance(self.type_, Unset):
            type_ = self.type_

        runtime: str | Unset = UNSET
        if not isinstance(self.runtime, Unset):
            runtime = self.runtime

        workspace_id = self.workspace_id

        postgres: dict[str, Any] | Unset = UNSET
        if not isinstance(self.postgres, Unset):
            postgres = self.postgres.to_dict()

        buckets: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.buckets, Unset):
            buckets = []
            for buckets_item_data in self.buckets:
                buckets_item = buckets_item_data.to_dict()
                buckets.append(buckets_item)

        lease_seconds = self.lease_seconds

        watch: dict[str, Any] | Unset = UNSET
        if not isinstance(self.watch, Unset):
            watch = self.watch.to_dict()

        go_command = self.go_command

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if type_ is not UNSET:
            field_dict["type"] = type_
        if runtime is not UNSET:
            field_dict["runtime"] = runtime
        if workspace_id is not UNSET:
            field_dict["workspace_id"] = workspace_id
        if postgres is not UNSET:
            field_dict["postgres"] = postgres
        if buckets is not UNSET:
            field_dict["buckets"] = buckets
        if lease_seconds is not UNSET:
            field_dict["lease_seconds"] = lease_seconds
        if watch is not UNSET:
            field_dict["watch"] = watch
        if go_command is not UNSET:
            field_dict["go_command"] = go_command

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.dev_bucket_request import DevBucketRequest
        from ..models.dev_postgres_request import DevPostgresRequest
        from ..models.dev_watch import DevWatch

        d = dict(src_dict)
        _type_ = d.pop("type", UNSET)
        type_: UpsertDevSessionRequestType | Unset
        if isinstance(_type_, Unset):
            type_ = UNSET
        else:
            type_ = check_upsert_dev_session_request_type(_type_)

        _runtime = d.pop("runtime", UNSET)
        runtime: UpsertDevSessionRequestRuntime | Unset
        if isinstance(_runtime, Unset):
            runtime = UNSET
        else:
            runtime = check_upsert_dev_session_request_runtime(_runtime)

        workspace_id = d.pop("workspace_id", UNSET)

        _postgres = d.pop("postgres", UNSET)
        postgres: DevPostgresRequest | Unset
        if isinstance(_postgres, Unset):
            postgres = UNSET
        else:
            postgres = DevPostgresRequest.from_dict(_postgres)

        _buckets = d.pop("buckets", UNSET)
        buckets: list[DevBucketRequest] | Unset = UNSET
        if _buckets is not UNSET:
            buckets = []
            for buckets_item_data in _buckets:
                buckets_item = DevBucketRequest.from_dict(buckets_item_data)

                buckets.append(buckets_item)

        lease_seconds = d.pop("lease_seconds", UNSET)

        _watch = d.pop("watch", UNSET)
        watch: DevWatch | Unset
        if isinstance(_watch, Unset):
            watch = UNSET
        else:
            watch = DevWatch.from_dict(_watch)

        go_command = d.pop("go_command", UNSET)

        upsert_dev_session_request = cls(
            type_=type_,
            runtime=runtime,
            workspace_id=workspace_id,
            postgres=postgres,
            buckets=buckets,
            lease_seconds=lease_seconds,
            watch=watch,
            go_command=go_command,
        )

        upsert_dev_session_request.additional_properties = d
        return upsert_dev_session_request

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
