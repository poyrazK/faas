from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    *,
    namespace: str,
    key: str,
    environment: str | Unset = UNSET,
    platform_tenant_id: str | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["namespace"] = namespace

    params["key"] = key

    params["environment"] = environment

    params["platform_tenant_id"] = platform_tenant_id

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/entities/connect".format(
            slug=quote(str(slug), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Any | Problem | None:
    if response.status_code == 101:
        response_101 = cast(Any, None)
        return response_101

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Response[Any | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    namespace: str,
    key: str,
    environment: str | Unset = UNSET,
    platform_tenant_id: str | Unset = UNSET,
) -> Response[Any | Problem]:
    """Upgrade a scoped durable entity session to WebSocket.

     Upgrade a scoped durable entity session to websocket: Service transport requires Bearer
    authentication, MFA, deploy write access and exact entity selectors. Origin headers are rejected.
    Every frame re-enters authentication and immutable-scope admission. Feature gates and the
    application allowlist must be enabled; native acceptance remains pending. The Node durable-session
    helper handles framing, replay and generation fences.

    Args:
        slug (str):
        namespace (str):
        key (str):
        environment (str | Unset):
        platform_tenant_id (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        namespace=namespace,
        key=key,
        environment=environment,
        platform_tenant_id=platform_tenant_id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    namespace: str,
    key: str,
    environment: str | Unset = UNSET,
    platform_tenant_id: str | Unset = UNSET,
) -> Any | Problem | None:
    """Upgrade a scoped durable entity session to WebSocket.

     Upgrade a scoped durable entity session to websocket: Service transport requires Bearer
    authentication, MFA, deploy write access and exact entity selectors. Origin headers are rejected.
    Every frame re-enters authentication and immutable-scope admission. Feature gates and the
    application allowlist must be enabled; native acceptance remains pending. The Node durable-session
    helper handles framing, replay and generation fences.

    Args:
        slug (str):
        namespace (str):
        key (str):
        environment (str | Unset):
        platform_tenant_id (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
        namespace=namespace,
        key=key,
        environment=environment,
        platform_tenant_id=platform_tenant_id,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    namespace: str,
    key: str,
    environment: str | Unset = UNSET,
    platform_tenant_id: str | Unset = UNSET,
) -> Response[Any | Problem]:
    """Upgrade a scoped durable entity session to WebSocket.

     Upgrade a scoped durable entity session to websocket: Service transport requires Bearer
    authentication, MFA, deploy write access and exact entity selectors. Origin headers are rejected.
    Every frame re-enters authentication and immutable-scope admission. Feature gates and the
    application allowlist must be enabled; native acceptance remains pending. The Node durable-session
    helper handles framing, replay and generation fences.

    Args:
        slug (str):
        namespace (str):
        key (str):
        environment (str | Unset):
        platform_tenant_id (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        namespace=namespace,
        key=key,
        environment=environment,
        platform_tenant_id=platform_tenant_id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    namespace: str,
    key: str,
    environment: str | Unset = UNSET,
    platform_tenant_id: str | Unset = UNSET,
) -> Any | Problem | None:
    """Upgrade a scoped durable entity session to WebSocket.

     Upgrade a scoped durable entity session to websocket: Service transport requires Bearer
    authentication, MFA, deploy write access and exact entity selectors. Origin headers are rejected.
    Every frame re-enters authentication and immutable-scope admission. Feature gates and the
    application allowlist must be enabled; native acceptance remains pending. The Node durable-session
    helper handles framing, replay and generation fences.

    Args:
        slug (str):
        namespace (str):
        key (str):
        environment (str | Unset):
        platform_tenant_id (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            namespace=namespace,
            key=key,
            environment=environment,
            platform_tenant_id=platform_tenant_id,
        )
    ).parsed
