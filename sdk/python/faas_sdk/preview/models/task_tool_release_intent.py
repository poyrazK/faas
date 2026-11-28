from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define

from ..models.task_tool_release_intent_access_mode import (
    TaskToolReleaseIntentAccessMode,
    check_task_tool_release_intent_access_mode,
)
from ..models.task_tool_release_intent_protocol import (
    TaskToolReleaseIntentProtocol,
    check_task_tool_release_intent_protocol,
)
from ..models.task_tool_release_intent_worker_runtime import (
    TaskToolReleaseIntentWorkerRuntime,
    check_task_tool_release_intent_worker_runtime,
)

if TYPE_CHECKING:
    from ..models.task_tool_release_app import TaskToolReleaseApp
    from ..models.task_tool_release_intent_tool_scopes import TaskToolReleaseIntentToolScopes


T = TypeVar("T", bound="TaskToolReleaseIntent")


@_attrs_define
class TaskToolReleaseIntent:
    """Immutable review metadata bounded to 64 KiB. Required setup remains unverified. No source paths, executable
    arguments, database URLs, credentials or credential references.

    """

    release_fingerprint: str
    worker_runtime: TaskToolReleaseIntentWorkerRuntime
    protocol: TaskToolReleaseIntentProtocol
    observer_metric_app: str
    endpoint: str
    access_mode: TaskToolReleaseIntentAccessMode
    tool_scopes: TaskToolReleaseIntentToolScopes
    apps: list[TaskToolReleaseApp]
    required_setup: list[str]
    reason: str

    def to_dict(self) -> dict[str, Any]:
        release_fingerprint = self.release_fingerprint

        worker_runtime: str = self.worker_runtime

        protocol: str = self.protocol

        observer_metric_app = self.observer_metric_app

        endpoint = self.endpoint

        access_mode: str = self.access_mode

        tool_scopes = self.tool_scopes.to_dict()

        apps = []
        for apps_item_data in self.apps:
            apps_item = apps_item_data.to_dict()
            apps.append(apps_item)

        required_setup = self.required_setup

        reason = self.reason

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "release_fingerprint": release_fingerprint,
                "worker_runtime": worker_runtime,
                "protocol": protocol,
                "observer_metric_app": observer_metric_app,
                "endpoint": endpoint,
                "access_mode": access_mode,
                "tool_scopes": tool_scopes,
                "apps": apps,
                "required_setup": required_setup,
                "reason": reason,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.task_tool_release_app import TaskToolReleaseApp
        from ..models.task_tool_release_intent_tool_scopes import TaskToolReleaseIntentToolScopes

        d = dict(src_dict)
        release_fingerprint = d.pop("release_fingerprint")

        worker_runtime = check_task_tool_release_intent_worker_runtime(d.pop("worker_runtime"))

        protocol = check_task_tool_release_intent_protocol(d.pop("protocol"))

        observer_metric_app = d.pop("observer_metric_app")

        endpoint = d.pop("endpoint")

        access_mode = check_task_tool_release_intent_access_mode(d.pop("access_mode"))

        tool_scopes = TaskToolReleaseIntentToolScopes.from_dict(d.pop("tool_scopes"))

        apps = []
        _apps = d.pop("apps")
        for apps_item_data in _apps:
            apps_item = TaskToolReleaseApp.from_dict(apps_item_data)

            apps.append(apps_item)

        required_setup = cast(list[str], d.pop("required_setup"))

        reason = d.pop("reason")

        task_tool_release_intent = cls(
            release_fingerprint=release_fingerprint,
            worker_runtime=worker_runtime,
            protocol=protocol,
            observer_metric_app=observer_metric_app,
            endpoint=endpoint,
            access_mode=access_mode,
            tool_scopes=tool_scopes,
            apps=apps,
            required_setup=required_setup,
            reason=reason,
        )

        return task_tool_release_intent
