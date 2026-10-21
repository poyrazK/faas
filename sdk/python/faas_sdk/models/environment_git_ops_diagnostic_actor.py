from typing import Literal

EnvironmentGitOpsDiagnosticActor = Literal["controller", "customer", "operator"]

ENVIRONMENT_GIT_OPS_DIAGNOSTIC_ACTOR_VALUES: set[EnvironmentGitOpsDiagnosticActor] = {
    "controller",
    "customer",
    "operator",
}


def check_environment_git_ops_diagnostic_actor(value: str) -> EnvironmentGitOpsDiagnosticActor:
    if value in ENVIRONMENT_GIT_OPS_DIAGNOSTIC_ACTOR_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {ENVIRONMENT_GIT_OPS_DIAGNOSTIC_ACTOR_VALUES!r}")
