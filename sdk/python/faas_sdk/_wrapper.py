"""faas_sdk._wrapper - `FaaSClient` constructor.

Thin convenience class that builds the generator's `Client` and
installs the wrapper BaseTransport chain on top of its inner
`httpx.Client`. Service calls go through the inner client::

    from faas_sdk.api.apps import list_apps
    apps = list_apps.sync(client=client.inner)

`client.inner` is the wrapped generator `Client`; `client.httpx_client`
is the chain-bearing `httpx.Client` for streaming and SSE.
"""

from __future__ import annotations

from dataclasses import dataclass, field
from typing import Any

import httpx

from ._transport import (
    RetryOptions,
    WrapperOptions,
    install_chain,
)
from .client import AuthenticatedClient
from .client import Client as _GenClient


@dataclass
class FaaSClientOptions:
    """Constructor kwargs for `FaaSClient`.

    `retry`, `logger`, and `httpx_args` are wrapper-only knobs.
    Every other kwarg is forwarded to the generator's `Client`.
    """

    retry: RetryOptions = field(default_factory=RetryOptions)
    logger: Any | None = None
    httpx_args: dict[str, Any] = field(default_factory=dict)


class FaaSClient:
    """Public sync facade for the one-box FaaS REST API.

    Construct once at process start, reuse across requests. Reachable
    service namespaces are accessed through the inner generator client::

        from faas_sdk.api.apps import list_apps
        from faas_sdk.api.account import get_v1_account

        client = FaaSClient(
            base_url="https://faas.example.com",
            token="...",  # optional; omit for anonymous endpoints
        )
        me = get_v1_account.sync(client=client.inner)
        apps = list_apps.sync(client=client.inner)
    """

    def __init__(
        self,
        *,
        base_url: str,
        token: str | None = None,
        verify_ssl: bool | str = True,
        timeout: float = 30.0,
        headers: dict[str, str] | None = None,
        follow_redirects: bool = False,
        options: FaaSClientOptions | None = None,
        **httpx_args: Any,
    ) -> None:
        opts = options or FaaSClientOptions()
        merged_args = {**opts.httpx_args, **httpx_args}

        if token:
            gen_client: _GenClient = AuthenticatedClient(
                base_url=base_url,
                token=token,
                verify_ssl=verify_ssl,
                timeout=timeout,
                headers=headers or {},
                follow_redirects=follow_redirects,
                **merged_args,
            )
        else:
            gen_client = _GenClient(
                base_url=base_url,
                verify_ssl=verify_ssl,
                timeout=timeout,
                headers=headers or {},
                follow_redirects=follow_redirects,
                **merged_args,
            )

        wrapper_opts = WrapperOptions(retry=opts.retry, logger=opts.logger)
        install_chain(gen_client, options=wrapper_opts, verify_ssl=verify_ssl)

        self._gen = gen_client
        self.options = opts

    @property
    def inner(self) -> _GenClient:
        """The wrapped generator `Client`. Use this for service
        calls::

            apps = client.apps.list_apps.sync(client=client.inner)
        """
        return self._gen

    @property
    def httpx_client(self) -> httpx.Client:
        """The chain-bearing underlying `httpx.Client`. Use this
        for `stream()` and SSE (see `faas_sdk.iter_sse`)."""
        return self._gen.get_httpx_client()

    def close(self) -> None:
        self._gen.get_httpx_client().close()

    def __enter__(self) -> FaaSClient:
        self._gen.__enter__()
        return self

    def __exit__(self, exc_type, exc, tb) -> None:
        self._gen.__exit__(exc_type, exc, tb)


__all__ = [
    "FaaSClient",
    "FaaSClientOptions",
]
