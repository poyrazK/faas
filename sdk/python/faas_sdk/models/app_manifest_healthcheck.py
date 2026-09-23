from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.sidecar_exec_probe import SidecarExecProbe
    from ..models.sidecar_http_get_probe import SidecarHTTPGetProbe
    from ..models.sidecar_tcp_socket_probe import SidecarTCPSocketProbe


T = TypeVar("T", bound="AppManifestHealthcheck")


@_attrs_define
class AppManifestHealthcheck:
    """AppManifest-level healthcheck shape: OCI HEALTHCHECK fields plus typed deployment probe overrides. Durations are
    integer seconds at the JSON boundary to match OCI/Docker conventions.

    """

    test: list[str] | Unset = UNSET
    """Argv of the check command, prefixed by "CMD", "CMD-SHELL", or "NONE" per Docker semantics."""
    exec_: SidecarExecProbe | Unset = UNSET
    """Exec probe command passed as argv inside the container; no shell is implied."""
    http_get: SidecarHTTPGetProbe | Unset = UNSET
    """HTTP GET probe sent from inside the container."""
    tcp_socket: SidecarTCPSocketProbe | Unset = UNSET
    """TCP connection probe opened from inside the container."""
    period_s: int | Unset = UNSET
    """Typed sidecar probe cadence in seconds; defaults to 10, or 30 for legacy OCI checks."""
    interval_s: int | None | Unset = UNSET
    """Poll cadence after StartPeriodS elapses (Docker default 30s)."""
    timeout_s: int | None | Unset = UNSET
    """Per-probe exec timeout (Docker default 30s)."""
    retries: int | None | Unset = UNSET
    """Consecutive failure count to mark unhealthy (Docker default 3)."""
    failure_threshold: int | Unset = UNSET
    """Failure count that marks a startup probe failed or restarts a liveness workload; defaults to 3."""
    success_threshold: int | Unset = UNSET
    """Consecutive passes required before the probe reports healthy; defaults to 1."""
    initial_delay_s: int | Unset = UNSET
    """Seconds to wait before the first typed sidecar probe."""
    start_period_s: int | None | Unset = UNSET
    """Startup grace during which failures don't count (Docker 17.05+, default 0s)."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        test: list[str] | Unset = UNSET
        if not isinstance(self.test, Unset):
            test = self.test

        exec_: dict[str, Any] | Unset = UNSET
        if not isinstance(self.exec_, Unset):
            exec_ = self.exec_.to_dict()

        http_get: dict[str, Any] | Unset = UNSET
        if not isinstance(self.http_get, Unset):
            http_get = self.http_get.to_dict()

        tcp_socket: dict[str, Any] | Unset = UNSET
        if not isinstance(self.tcp_socket, Unset):
            tcp_socket = self.tcp_socket.to_dict()

        period_s = self.period_s

        interval_s: int | None | Unset
        if isinstance(self.interval_s, Unset):
            interval_s = UNSET
        else:
            interval_s = self.interval_s

        timeout_s: int | None | Unset
        if isinstance(self.timeout_s, Unset):
            timeout_s = UNSET
        else:
            timeout_s = self.timeout_s

        retries: int | None | Unset
        if isinstance(self.retries, Unset):
            retries = UNSET
        else:
            retries = self.retries

        failure_threshold = self.failure_threshold

        success_threshold = self.success_threshold

        initial_delay_s = self.initial_delay_s

        start_period_s: int | None | Unset
        if isinstance(self.start_period_s, Unset):
            start_period_s = UNSET
        else:
            start_period_s = self.start_period_s

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if test is not UNSET:
            field_dict["test"] = test
        if exec_ is not UNSET:
            field_dict["exec"] = exec_
        if http_get is not UNSET:
            field_dict["http_get"] = http_get
        if tcp_socket is not UNSET:
            field_dict["tcp_socket"] = tcp_socket
        if period_s is not UNSET:
            field_dict["period_s"] = period_s
        if interval_s is not UNSET:
            field_dict["interval_s"] = interval_s
        if timeout_s is not UNSET:
            field_dict["timeout_s"] = timeout_s
        if retries is not UNSET:
            field_dict["retries"] = retries
        if failure_threshold is not UNSET:
            field_dict["failure_threshold"] = failure_threshold
        if success_threshold is not UNSET:
            field_dict["success_threshold"] = success_threshold
        if initial_delay_s is not UNSET:
            field_dict["initial_delay_s"] = initial_delay_s
        if start_period_s is not UNSET:
            field_dict["start_period_s"] = start_period_s

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.sidecar_exec_probe import SidecarExecProbe
        from ..models.sidecar_http_get_probe import SidecarHTTPGetProbe
        from ..models.sidecar_tcp_socket_probe import SidecarTCPSocketProbe

        d = dict(src_dict)
        test = cast(list[str], d.pop("test", UNSET))

        _exec_ = d.pop("exec", UNSET)
        exec_: SidecarExecProbe | Unset
        if isinstance(_exec_, Unset):
            exec_ = UNSET
        else:
            exec_ = SidecarExecProbe.from_dict(_exec_)

        _http_get = d.pop("http_get", UNSET)
        http_get: SidecarHTTPGetProbe | Unset
        if isinstance(_http_get, Unset):
            http_get = UNSET
        else:
            http_get = SidecarHTTPGetProbe.from_dict(_http_get)

        _tcp_socket = d.pop("tcp_socket", UNSET)
        tcp_socket: SidecarTCPSocketProbe | Unset
        if isinstance(_tcp_socket, Unset):
            tcp_socket = UNSET
        else:
            tcp_socket = SidecarTCPSocketProbe.from_dict(_tcp_socket)

        period_s = d.pop("period_s", UNSET)

        def _parse_interval_s(data: object) -> int | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(int | None | Unset, data)

        interval_s = _parse_interval_s(d.pop("interval_s", UNSET))

        def _parse_timeout_s(data: object) -> int | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(int | None | Unset, data)

        timeout_s = _parse_timeout_s(d.pop("timeout_s", UNSET))

        def _parse_retries(data: object) -> int | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(int | None | Unset, data)

        retries = _parse_retries(d.pop("retries", UNSET))

        failure_threshold = d.pop("failure_threshold", UNSET)

        success_threshold = d.pop("success_threshold", UNSET)

        initial_delay_s = d.pop("initial_delay_s", UNSET)

        def _parse_start_period_s(data: object) -> int | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(int | None | Unset, data)

        start_period_s = _parse_start_period_s(d.pop("start_period_s", UNSET))

        app_manifest_healthcheck = cls(
            test=test,
            exec_=exec_,
            http_get=http_get,
            tcp_socket=tcp_socket,
            period_s=period_s,
            interval_s=interval_s,
            timeout_s=timeout_s,
            retries=retries,
            failure_threshold=failure_threshold,
            success_threshold=success_threshold,
            initial_delay_s=initial_delay_s,
            start_period_s=start_period_s,
        )

        app_manifest_healthcheck.additional_properties = d
        return app_manifest_healthcheck

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
