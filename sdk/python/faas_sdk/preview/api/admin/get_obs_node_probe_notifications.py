from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.obs_node_probe_notifications import ObsNodeProbeNotifications
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    name: str,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/admin/obs/nodes/{name}/probe-notifications".format(
            name=quote(str(name), safe=""),
        ),
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Any | ObsNodeProbeNotifications | Problem | None:
    if response.status_code == 200:
        response_200 = ObsNodeProbeNotifications.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = cast(Any, None)
        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 403:
        response_403 = cast(Any, None)
        return response_403

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Any | ObsNodeProbeNotifications | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    name: str,
    *,
    client: AuthenticatedClient | Client,
) -> Response[Any | ObsNodeProbeNotifications | Problem]:
    """Inspect both direct notification channels for one expected node.

     Requires operator read scope, MFA and the operator allowlist. Accepts a
    stable node name with no query parameters. Reads independent inventory
    and then node-filtered notification recordings under one pinned time
    and shared three-second deadline. It does not filter the truncated
    fleet preview. Returns both channels when the node is expected, even
    when their outcomes are unknown. Missing or unconfigured inventory has
    expected null; fresh configured inventory without that node has
    expected false and not_applicable. This is monitoring intent, not proof
    of registry existence. No writes, direct probes, messages or credential
    reads occur. Historical submissions do not prove current reachability,
    provider processing or human receipt. Raw URLs, addresses, arbitrary
    source labels and provider text are excluded.

    Args:
        name (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | ObsNodeProbeNotifications | Problem]
    """

    kwargs = _get_kwargs(
        name=name,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    name: str,
    *,
    client: AuthenticatedClient | Client,
) -> Any | ObsNodeProbeNotifications | Problem | None:
    """Inspect both direct notification channels for one expected node.

     Requires operator read scope, MFA and the operator allowlist. Accepts a
    stable node name with no query parameters. Reads independent inventory
    and then node-filtered notification recordings under one pinned time
    and shared three-second deadline. It does not filter the truncated
    fleet preview. Returns both channels when the node is expected, even
    when their outcomes are unknown. Missing or unconfigured inventory has
    expected null; fresh configured inventory without that node has
    expected false and not_applicable. This is monitoring intent, not proof
    of registry existence. No writes, direct probes, messages or credential
    reads occur. Historical submissions do not prove current reachability,
    provider processing or human receipt. Raw URLs, addresses, arbitrary
    source labels and provider text are excluded.

    Args:
        name (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | ObsNodeProbeNotifications | Problem
    """

    return sync_detailed(
        name=name,
        client=client,
    ).parsed


async def asyncio_detailed(
    name: str,
    *,
    client: AuthenticatedClient | Client,
) -> Response[Any | ObsNodeProbeNotifications | Problem]:
    """Inspect both direct notification channels for one expected node.

     Requires operator read scope, MFA and the operator allowlist. Accepts a
    stable node name with no query parameters. Reads independent inventory
    and then node-filtered notification recordings under one pinned time
    and shared three-second deadline. It does not filter the truncated
    fleet preview. Returns both channels when the node is expected, even
    when their outcomes are unknown. Missing or unconfigured inventory has
    expected null; fresh configured inventory without that node has
    expected false and not_applicable. This is monitoring intent, not proof
    of registry existence. No writes, direct probes, messages or credential
    reads occur. Historical submissions do not prove current reachability,
    provider processing or human receipt. Raw URLs, addresses, arbitrary
    source labels and provider text are excluded.

    Args:
        name (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | ObsNodeProbeNotifications | Problem]
    """

    kwargs = _get_kwargs(
        name=name,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    name: str,
    *,
    client: AuthenticatedClient | Client,
) -> Any | ObsNodeProbeNotifications | Problem | None:
    """Inspect both direct notification channels for one expected node.

     Requires operator read scope, MFA and the operator allowlist. Accepts a
    stable node name with no query parameters. Reads independent inventory
    and then node-filtered notification recordings under one pinned time
    and shared three-second deadline. It does not filter the truncated
    fleet preview. Returns both channels when the node is expected, even
    when their outcomes are unknown. Missing or unconfigured inventory has
    expected null; fresh configured inventory without that node has
    expected false and not_applicable. This is monitoring intent, not proof
    of registry existence. No writes, direct probes, messages or credential
    reads occur. Historical submissions do not prove current reachability,
    provider processing or human receipt. Raw URLs, addresses, arbitrary
    source labels and provider text are excluded.

    Args:
        name (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | ObsNodeProbeNotifications | Problem
    """

    return (
        await asyncio_detailed(
            name=name,
            client=client,
        )
    ).parsed
