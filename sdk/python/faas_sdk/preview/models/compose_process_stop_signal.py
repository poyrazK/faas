from typing import Literal

ComposeProcessStopSignal = Literal["SIGHUP", "SIGINT", "SIGQUIT", "SIGTERM", "SIGUSR1", "SIGUSR2"]

COMPOSE_PROCESS_STOP_SIGNAL_VALUES: set[ComposeProcessStopSignal] = {
    "SIGHUP",
    "SIGINT",
    "SIGQUIT",
    "SIGTERM",
    "SIGUSR1",
    "SIGUSR2",
}


def check_compose_process_stop_signal(value: str) -> ComposeProcessStopSignal:
    if value in COMPOSE_PROCESS_STOP_SIGNAL_VALUES:
        return value
    raise TypeError(f"Unexpected value {value!r}. Expected one of {COMPOSE_PROCESS_STOP_SIGNAL_VALUES!r}")
