from typing import Literal

IssueSourceMapManifestVersion = Literal[1]

ISSUE_SOURCE_MAP_MANIFEST_VERSION_VALUES: set[IssueSourceMapManifestVersion] = {
    1,
}


def check_issue_source_map_manifest_version(value: int) -> IssueSourceMapManifestVersion:
    if value in ISSUE_SOURCE_MAP_MANIFEST_VERSION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ISSUE_SOURCE_MAP_MANIFEST_VERSION_VALUES!r}")
