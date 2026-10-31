from typing import Literal

DeploymentResponseDeployedViaType2Type1 = Literal["api", "cli", "dashboard", "github", "operator"]

DEPLOYMENT_RESPONSE_DEPLOYED_VIA_TYPE_2_TYPE_1_VALUES: set[DeploymentResponseDeployedViaType2Type1] = {
    "api",
    "cli",
    "dashboard",
    "github",
    "operator",
}


def check_deployment_response_deployed_via_type_2_type_1(value: str) -> DeploymentResponseDeployedViaType2Type1:
    if value in DEPLOYMENT_RESPONSE_DEPLOYED_VIA_TYPE_2_TYPE_1_VALUES:
        return value
    raise TypeError(
        f"Unexpected value {value!r}. Expected one of {DEPLOYMENT_RESPONSE_DEPLOYED_VIA_TYPE_2_TYPE_1_VALUES!r}"
    )
