from typing import Literal

ToolPublicationReadinessRoleCatalogFormat = Literal[1, 2]

TOOL_PUBLICATION_READINESS_ROLE_CATALOG_FORMAT_VALUES: set[ToolPublicationReadinessRoleCatalogFormat] = {
    1,
    2,
}


def check_tool_publication_readiness_role_catalog_format(value: int) -> ToolPublicationReadinessRoleCatalogFormat:
    if value in TOOL_PUBLICATION_READINESS_ROLE_CATALOG_FORMAT_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {TOOL_PUBLICATION_READINESS_ROLE_CATALOG_FORMAT_VALUES!r}"
    )
