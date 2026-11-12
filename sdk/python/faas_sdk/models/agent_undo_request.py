from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="AgentUndoRequest")


@_attrs_define
class AgentUndoRequest:
    """Chooses an agent's changes to undo: those since a time (default the last 24 hours), or the activity entries named."""

    since: datetime.datetime | Unset = UNSET
    """Undo changes made at or after this time; at most 7 days ago."""
    entries: list[str] | Unset = UNSET
    """Activity entry ids to undo; when given, only they are undone."""
    force: bool | Unset = UNSET
    """Also undo changes someone changed again since the agent did."""
    plan_digest: str | Unset = UNSET
    """On apply, the plan_digest of the reviewed plan."""

    def to_dict(self) -> dict[str, Any]:
        since: str | Unset = UNSET
        if not isinstance(self.since, Unset):
            since = self.since.isoformat()

        entries: list[str] | Unset = UNSET
        if not isinstance(self.entries, Unset):
            entries = self.entries

        force = self.force

        plan_digest = self.plan_digest

        field_dict: dict[str, Any] = {}

        field_dict.update({})
        if since is not UNSET:
            field_dict["since"] = since
        if entries is not UNSET:
            field_dict["entries"] = entries
        if force is not UNSET:
            field_dict["force"] = force
        if plan_digest is not UNSET:
            field_dict["plan_digest"] = plan_digest

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        _since = d.pop("since", UNSET)
        since: datetime.datetime | Unset
        if isinstance(_since, Unset):
            since = UNSET
        else:
            since = datetime.datetime.fromisoformat(_since)

        entries = cast(list[str], d.pop("entries", UNSET))

        force = d.pop("force", UNSET)

        plan_digest = d.pop("plan_digest", UNSET)

        agent_undo_request = cls(
            since=since,
            entries=entries,
            force=force,
            plan_digest=plan_digest,
        )

        return agent_undo_request
