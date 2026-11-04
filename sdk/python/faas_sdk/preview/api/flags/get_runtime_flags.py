from http import HTTPStatus
from typing import Any, cast

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.flags_bundle import FlagsBundle
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    *,
    x_faas_flags_capabilities: str | Unset = UNSET,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}
    if not isinstance(x_faas_flags_capabilities, Unset):
        headers["X-Faas-Flags-Capabilities"] = x_faas_flags_capabilities

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/runtime/flags",
    }

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Any | FlagsBundle | Problem | None:
    if response.status_code == 200:
        response_200 = FlagsBundle.from_dict(response.json())

        return response_200

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 422:
        response_422 = Problem.from_dict(response.json())

        return response_422

    if response.status_code == 426:
        response_426 = cast(Any, None)
        return response_426

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Any | FlagsBundle | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient,
    x_faas_flags_capabilities: str | Unset = UNSET,
) -> Response[Any | FlagsBundle | Problem]:
    """Read the live workload’s project-environment flag bundle.

     Requires an RS256 workload identity token with audience gregale:flags from an active app instance.
    Project and environment are derived from the deployment; query parameters cannot select another
    scope. Clients advertise runtime evaluation capabilities; a bundle using a capability is withheld
    from older clients that do not advertise it.

    Args:
        x_faas_flags_capabilities (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | FlagsBundle | Problem]
    """

    kwargs = _get_kwargs(
        x_faas_flags_capabilities=x_faas_flags_capabilities,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient,
    x_faas_flags_capabilities: str | Unset = UNSET,
) -> Any | FlagsBundle | Problem | None:
    """Read the live workload’s project-environment flag bundle.

     Requires an RS256 workload identity token with audience gregale:flags from an active app instance.
    Project and environment are derived from the deployment; query parameters cannot select another
    scope. Clients advertise runtime evaluation capabilities; a bundle using a capability is withheld
    from older clients that do not advertise it.

    Args:
        x_faas_flags_capabilities (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | FlagsBundle | Problem
    """

    return sync_detailed(
        client=client,
        x_faas_flags_capabilities=x_faas_flags_capabilities,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient,
    x_faas_flags_capabilities: str | Unset = UNSET,
) -> Response[Any | FlagsBundle | Problem]:
    """Read the live workload’s project-environment flag bundle.

     Requires an RS256 workload identity token with audience gregale:flags from an active app instance.
    Project and environment are derived from the deployment; query parameters cannot select another
    scope. Clients advertise runtime evaluation capabilities; a bundle using a capability is withheld
    from older clients that do not advertise it.

    Args:
        x_faas_flags_capabilities (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | FlagsBundle | Problem]
    """

    kwargs = _get_kwargs(
        x_faas_flags_capabilities=x_faas_flags_capabilities,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient,
    x_faas_flags_capabilities: str | Unset = UNSET,
) -> Any | FlagsBundle | Problem | None:
    """Read the live workload’s project-environment flag bundle.

     Requires an RS256 workload identity token with audience gregale:flags from an active app instance.
    Project and environment are derived from the deployment; query parameters cannot select another
    scope. Clients advertise runtime evaluation capabilities; a bundle using a capability is withheld
    from older clients that do not advertise it.

    Args:
        x_faas_flags_capabilities (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | FlagsBundle | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
            x_faas_flags_capabilities=x_faas_flags_capabilities,
        )
    ).parsed
