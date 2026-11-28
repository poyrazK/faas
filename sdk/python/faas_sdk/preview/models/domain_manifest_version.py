from typing import Literal

DomainManifestVersion = Literal[1]

DOMAIN_MANIFEST_VERSION_VALUES: set[DomainManifestVersion] = {
    1,
}


def check_domain_manifest_version(value: int) -> DomainManifestVersion:
    if value in DOMAIN_MANIFEST_VERSION_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {DOMAIN_MANIFEST_VERSION_VALUES!r}")
