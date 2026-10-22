from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.release_diff_code import ReleaseDiffCode
    from ..models.release_diff_config import ReleaseDiffConfig
    from ..models.release_diff_database import ReleaseDiffDatabase
    from ..models.release_diff_dependencies import ReleaseDiffDependencies
    from ..models.release_diff_field import ReleaseDiffField
    from ..models.release_diff_side import ReleaseDiffSide


T = TypeVar("T", bound="ReleaseDiff")


@_attrs_define
class ReleaseDiff:
    """What changed between two releases of one app (ADR-20261021101117)."""

    app: str
    to: ReleaseDiffSide
    """One of the two compared releases."""
    summary: str
    """One line, such as "new code, 2 config keys, 5 packages"."""
    code: ReleaseDiffCode
    """How the releases' source and image differ."""
    config: ReleaseDiffConfig
    """How the configuration each release started with differs, by key."""
    dependencies: ReleaseDiffDependencies
    """How the builds' SBOM packages differ. Each list holds at most 100 entries."""
    runtime: list[ReleaseDiffField]
    database: ReleaseDiffDatabase
    """How the releases' release commands, which run database migrations, differ (ADR-1448)."""
    from_: ReleaseDiffSide | Unset = UNSET
    """One of the two compared releases."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        app = self.app

        to = self.to.to_dict()

        summary = self.summary

        code = self.code.to_dict()

        config = self.config.to_dict()

        dependencies = self.dependencies.to_dict()

        runtime = []
        for runtime_item_data in self.runtime:
            runtime_item = runtime_item_data.to_dict()
            runtime.append(runtime_item)

        database = self.database.to_dict()

        from_: dict[str, Any] | Unset = UNSET
        if not isinstance(self.from_, Unset):
            from_ = self.from_.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "app": app,
                "to": to,
                "summary": summary,
                "code": code,
                "config": config,
                "dependencies": dependencies,
                "runtime": runtime,
                "database": database,
            }
        )
        if from_ is not UNSET:
            field_dict["from"] = from_

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.release_diff_code import ReleaseDiffCode
        from ..models.release_diff_config import ReleaseDiffConfig
        from ..models.release_diff_database import ReleaseDiffDatabase
        from ..models.release_diff_dependencies import ReleaseDiffDependencies
        from ..models.release_diff_field import ReleaseDiffField
        from ..models.release_diff_side import ReleaseDiffSide

        d = dict(src_dict)
        app = d.pop("app")

        to = ReleaseDiffSide.from_dict(d.pop("to"))

        summary = d.pop("summary")

        code = ReleaseDiffCode.from_dict(d.pop("code"))

        config = ReleaseDiffConfig.from_dict(d.pop("config"))

        dependencies = ReleaseDiffDependencies.from_dict(d.pop("dependencies"))

        runtime = []
        _runtime = d.pop("runtime")
        for runtime_item_data in _runtime:
            runtime_item = ReleaseDiffField.from_dict(runtime_item_data)

            runtime.append(runtime_item)

        database = ReleaseDiffDatabase.from_dict(d.pop("database"))

        _from_ = d.pop("from", UNSET)
        from_: ReleaseDiffSide | Unset
        if isinstance(_from_, Unset):
            from_ = UNSET
        else:
            from_ = ReleaseDiffSide.from_dict(_from_)

        release_diff = cls(
            app=app,
            to=to,
            summary=summary,
            code=code,
            config=config,
            dependencies=dependencies,
            runtime=runtime,
            database=database,
            from_=from_,
        )

        release_diff.additional_properties = d
        return release_diff

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
