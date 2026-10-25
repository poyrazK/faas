from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.profiling_config import ProfilingConfig
    from ..models.tracing_config import TracingConfig


T = TypeVar("T", bound="DeploymentStagingRequestBefore")


@_attrs_define
class DeploymentStagingRequestBefore:
    profiling: None | ProfilingConfig | Unset = UNSET
    tracing: None | TracingConfig | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        from ..models.profiling_config import ProfilingConfig
        from ..models.tracing_config import TracingConfig

        profiling: dict[str, Any] | None | Unset
        if isinstance(self.profiling, Unset):
            profiling = UNSET
        elif isinstance(self.profiling, ProfilingConfig):
            profiling = self.profiling.to_dict()
        else:
            profiling = self.profiling

        tracing: dict[str, Any] | None | Unset
        if isinstance(self.tracing, Unset):
            tracing = UNSET
        elif isinstance(self.tracing, TracingConfig):
            tracing = self.tracing.to_dict()
        else:
            tracing = self.tracing

        field_dict: dict[str, Any] = {}

        field_dict.update({})
        if profiling is not UNSET:
            field_dict["profiling"] = profiling
        if tracing is not UNSET:
            field_dict["tracing"] = tracing

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.profiling_config import ProfilingConfig
        from ..models.tracing_config import TracingConfig

        d = dict(src_dict)

        def _parse_profiling(data: object) -> None | ProfilingConfig | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, dict):
                    raise TypeError()
                profiling_type_0 = ProfilingConfig.from_dict(data)

                return profiling_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(None | ProfilingConfig | Unset, data)

        profiling = _parse_profiling(d.pop("profiling", UNSET))

        def _parse_tracing(data: object) -> None | TracingConfig | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, dict):
                    raise TypeError()
                tracing_type_0 = TracingConfig.from_dict(data)

                return tracing_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(None | TracingConfig | Unset, data)

        tracing = _parse_tracing(d.pop("tracing", UNSET))

        deployment_staging_request_before = cls(
            profiling=profiling,
            tracing=tracing,
        )

        return deployment_staging_request_before
