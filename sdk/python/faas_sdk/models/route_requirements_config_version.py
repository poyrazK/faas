from typing import Literal

RouteRequirementsConfigVersion = Literal[1, 2]

ROUTE_REQUIREMENTS_CONFIG_VERSION_VALUES: set[RouteRequirementsConfigVersion] = {
    1,
    2,
}


def check_route_requirements_config_version(value: int) -> RouteRequirementsConfigVersion:
    if value in ROUTE_REQUIREMENTS_CONFIG_VERSION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ROUTE_REQUIREMENTS_CONFIG_VERSION_VALUES!r}")
