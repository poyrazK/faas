from typing import Literal

ListOrgActivityActorType = Literal["api_key", "github", "operator", "system", "user"]

LIST_ORG_ACTIVITY_ACTOR_TYPE_VALUES: set[ListOrgActivityActorType] = {
    "api_key",
    "github",
    "operator",
    "system",
    "user",
}


def check_list_org_activity_actor_type(value: str) -> ListOrgActivityActorType:
    if value in LIST_ORG_ACTIVITY_ACTOR_TYPE_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {LIST_ORG_ACTIVITY_ACTOR_TYPE_VALUES!r}")
