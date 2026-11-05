from typing import Literal

IncidentNoteKind = Literal["acknowledged", "note"]

INCIDENT_NOTE_KIND_VALUES: set[IncidentNoteKind] = {
    "acknowledged",
    "note",
}


def check_incident_note_kind(value: str) -> IncidentNoteKind:
    if value in INCIDENT_NOTE_KIND_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {INCIDENT_NOTE_KIND_VALUES!r}")
