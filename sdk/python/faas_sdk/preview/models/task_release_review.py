from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.task_release_review_status import TaskReleaseReviewStatus, check_task_release_review_status
from ..models.task_release_review_version import TaskReleaseReviewVersion, check_task_release_review_version
from ..models.task_release_review_worker_runtime import (
    TaskReleaseReviewWorkerRuntime,
    check_task_release_review_worker_runtime,
)

if TYPE_CHECKING:
    from ..models.task_release_review_tool_scopes import TaskReleaseReviewToolScopes
    from ..models.task_review_limits import TaskReviewLimits


T = TypeVar("T", bound="TaskReleaseReview")


@_attrs_define
class TaskReleaseReview:
    """Offline credential-free Task source review; never executable."""

    version: TaskReleaseReviewVersion
    status: TaskReleaseReviewStatus
    release_fingerprint: str
    protocol: str
    web_app: str
    worker_app: str
    worker_runtime: TaskReleaseReviewWorkerRuntime
    observer_app: str
    observer_metric_app: str
    previous_worker_apps: list[str]
    endpoint: str
    access_mode: str
    tool_scopes: TaskReleaseReviewToolScopes
    readiness_roles: list[str]
    required_setup: list[str]
    release_timeout_seconds: int
    web_configured_limits: TaskReviewLimits
    """Explicit configured Task overrides only; no binding or readiness evidence."""
    worker_configured_limits: TaskReviewLimits
    """Explicit configured Task overrides only; no binding or readiness evidence."""
    migrations_required: bool
    """True because deploying the reviewed Task stack still requires its separately authorized migration workflow."""
    bindings_verified: bool
    """False because offline source review does not resolve database, key or namespace bindings."""
    ready_to_apply: bool
    """False because an offline Task review is not an authorized executable release plan."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        version: int = self.version

        status: str = self.status

        release_fingerprint = self.release_fingerprint

        protocol = self.protocol

        web_app = self.web_app

        worker_app = self.worker_app

        worker_runtime: str = self.worker_runtime

        observer_app = self.observer_app

        observer_metric_app = self.observer_metric_app

        previous_worker_apps = self.previous_worker_apps

        endpoint = self.endpoint

        access_mode = self.access_mode

        tool_scopes = self.tool_scopes.to_dict()

        readiness_roles = self.readiness_roles

        required_setup = self.required_setup

        release_timeout_seconds = self.release_timeout_seconds

        web_configured_limits = self.web_configured_limits.to_dict()

        worker_configured_limits = self.worker_configured_limits.to_dict()

        migrations_required = self.migrations_required

        bindings_verified = self.bindings_verified

        ready_to_apply = self.ready_to_apply

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "version": version,
                "status": status,
                "release_fingerprint": release_fingerprint,
                "protocol": protocol,
                "web_app": web_app,
                "worker_app": worker_app,
                "worker_runtime": worker_runtime,
                "observer_app": observer_app,
                "observer_metric_app": observer_metric_app,
                "previous_worker_apps": previous_worker_apps,
                "endpoint": endpoint,
                "access_mode": access_mode,
                "tool_scopes": tool_scopes,
                "readiness_roles": readiness_roles,
                "required_setup": required_setup,
                "release_timeout_seconds": release_timeout_seconds,
                "web_configured_limits": web_configured_limits,
                "worker_configured_limits": worker_configured_limits,
                "migrations_required": migrations_required,
                "bindings_verified": bindings_verified,
                "ready_to_apply": ready_to_apply,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.task_release_review_tool_scopes import TaskReleaseReviewToolScopes
        from ..models.task_review_limits import TaskReviewLimits

        d = dict(src_dict)
        version = check_task_release_review_version(d.pop("version"))

        status = check_task_release_review_status(d.pop("status"))

        release_fingerprint = d.pop("release_fingerprint")

        protocol = d.pop("protocol")

        web_app = d.pop("web_app")

        worker_app = d.pop("worker_app")

        worker_runtime = check_task_release_review_worker_runtime(d.pop("worker_runtime"))

        observer_app = d.pop("observer_app")

        observer_metric_app = d.pop("observer_metric_app")

        previous_worker_apps = cast(list[str], d.pop("previous_worker_apps"))

        endpoint = d.pop("endpoint")

        access_mode = d.pop("access_mode")

        tool_scopes = TaskReleaseReviewToolScopes.from_dict(d.pop("tool_scopes"))

        readiness_roles = cast(list[str], d.pop("readiness_roles"))

        required_setup = cast(list[str], d.pop("required_setup"))

        release_timeout_seconds = d.pop("release_timeout_seconds")

        web_configured_limits = TaskReviewLimits.from_dict(d.pop("web_configured_limits"))

        worker_configured_limits = TaskReviewLimits.from_dict(d.pop("worker_configured_limits"))

        migrations_required = d.pop("migrations_required")

        bindings_verified = d.pop("bindings_verified")

        ready_to_apply = d.pop("ready_to_apply")

        task_release_review = cls(
            version=version,
            status=status,
            release_fingerprint=release_fingerprint,
            protocol=protocol,
            web_app=web_app,
            worker_app=worker_app,
            worker_runtime=worker_runtime,
            observer_app=observer_app,
            observer_metric_app=observer_metric_app,
            previous_worker_apps=previous_worker_apps,
            endpoint=endpoint,
            access_mode=access_mode,
            tool_scopes=tool_scopes,
            readiness_roles=readiness_roles,
            required_setup=required_setup,
            release_timeout_seconds=release_timeout_seconds,
            web_configured_limits=web_configured_limits,
            worker_configured_limits=worker_configured_limits,
            migrations_required=migrations_required,
            bindings_verified=bindings_verified,
            ready_to_apply=ready_to_apply,
        )

        task_release_review.additional_properties = d
        return task_release_review

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
