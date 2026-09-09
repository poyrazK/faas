from typing import Literal

AppManifestCrawlerPolicy = Literal["block", "cached", "wake"]

APP_MANIFEST_CRAWLER_POLICY_VALUES: set[AppManifestCrawlerPolicy] = {
    "block",
    "cached",
    "wake",
}


def check_app_manifest_crawler_policy(value: str) -> AppManifestCrawlerPolicy:
    if value in APP_MANIFEST_CRAWLER_POLICY_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {APP_MANIFEST_CRAWLER_POLICY_VALUES!r}")
