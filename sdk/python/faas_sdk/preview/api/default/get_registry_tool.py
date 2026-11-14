from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.tool_registry_tool import ToolRegistryTool
from ...types import Response


def _get_kwargs(
    version: str,
    name: str,
) -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/tool-registry/{version}/tools/{name}".format(
            version=quote(str(version), safe=""),
            name=quote(str(name), safe=""),
        ),
    }

    return _kwargs


def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> Any | ToolRegistryTool | None:
    if response.status_code == 200:
        response_200 = ToolRegistryTool.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = cast(Any, None)
        return response_400

    if response.status_code == 403:
        response_403 = cast(Any, None)
        return response_403

    if response.status_code == 404:
        response_404 = cast(Any, None)
        return response_404

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Any | ToolRegistryTool]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    version: str,
    name: str,
    *,
    client: AuthenticatedClient | Client,
) -> Response[Any | ToolRegistryTool]:
    """Read a single-tool catalog projection; apps-read and exact delegated access required for agents

     Returns only the selected tool and its declared application scopes. Agents require a current exact
    grant, live personal connection key and matching current app/project identities. Full publication
    catalogs and unrelated test evidence remain owner-only. No execution or runtime availability is
    implied.

    Args:
        version (str):
        name (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | ToolRegistryTool]
    """

    kwargs = _get_kwargs(
        version=version,
        name=name,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    version: str,
    name: str,
    *,
    client: AuthenticatedClient | Client,
) -> Any | ToolRegistryTool | None:
    """Read a single-tool catalog projection; apps-read and exact delegated access required for agents

     Returns only the selected tool and its declared application scopes. Agents require a current exact
    grant, live personal connection key and matching current app/project identities. Full publication
    catalogs and unrelated test evidence remain owner-only. No execution or runtime availability is
    implied.

    Args:
        version (str):
        name (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | ToolRegistryTool
    """

    return sync_detailed(
        version=version,
        name=name,
        client=client,
    ).parsed


async def asyncio_detailed(
    version: str,
    name: str,
    *,
    client: AuthenticatedClient | Client,
) -> Response[Any | ToolRegistryTool]:
    """Read a single-tool catalog projection; apps-read and exact delegated access required for agents

     Returns only the selected tool and its declared application scopes. Agents require a current exact
    grant, live personal connection key and matching current app/project identities. Full publication
    catalogs and unrelated test evidence remain owner-only. No execution or runtime availability is
    implied.

    Args:
        version (str):
        name (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | ToolRegistryTool]
    """

    kwargs = _get_kwargs(
        version=version,
        name=name,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    version: str,
    name: str,
    *,
    client: AuthenticatedClient | Client,
) -> Any | ToolRegistryTool | None:
    """Read a single-tool catalog projection; apps-read and exact delegated access required for agents

     Returns only the selected tool and its declared application scopes. Agents require a current exact
    grant, live personal connection key and matching current app/project identities. Full publication
    catalogs and unrelated test evidence remain owner-only. No execution or runtime availability is
    implied.

    Args:
        version (str):
        name (str):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | ToolRegistryTool
    """

    return (
        await asyncio_detailed(
            version=version,
            name=name,
            client=client,
        )
    ).parsed
