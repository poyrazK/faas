from typing import Literal

DeploymentResponseDeployedViaType3Type1 = Literal["api", "cli", "dashboard", "github", "operator"]

DEPLOYMENT_RESPONSE_DEPLOYED_VIA_TYPE_3_TYPE_1_VALUES: set[DeploymentResponseDeployedViaType3Type1] = {
    "api",
    "cli",
    "dashboard",
    "github",
    "operator",
}


def check_deployment_response_deployed_via_type_3_type_1(value: str) -> DeploymentResponseDeployedViaType3Type1:
    if value in DEPLOYMENT_RESPONSE_DEPLOYED_VIA_TYPE_3_TYPE_1_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {DEPLOYMENT_RESPONSE_DEPLOYED_VIA_TYPE_3_TYPE_1_VALUES!r}"
    )
