from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.get_open_api_spec_json_response_200 import GetOpenAPISpecJSONResponse200
from ...types import Response


def _get_kwargs() -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/openapi.json",
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> GetOpenAPISpecJSONResponse200 | None:
    if response.status_code == 200:
        response_200 = GetOpenAPISpecJSONResponse200.from_dict(response.json())

        return response_200

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[GetOpenAPISpecJSONResponse200]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
) -> Response[GetOpenAPISpecJSONResponse200]:
    """This spec, as JSON.

     Same spec, YAML parsed and re-emitted as JSON. Preferred by
    SDK generators (`openapi-generator`, `oapi-codegen`).

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[GetOpenAPISpecJSONResponse200]
    """

    kwargs = _get_kwargs()

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
) -> GetOpenAPISpecJSONResponse200 | None:
    """This spec, as JSON.

     Same spec, YAML parsed and re-emitted as JSON. Preferred by
    SDK generators (`openapi-generator`, `oapi-codegen`).

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        GetOpenAPISpecJSONResponse200
    """

    return sync_detailed(
        client=client,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
) -> Response[GetOpenAPISpecJSONResponse200]:
    """This spec, as JSON.

     Same spec, YAML parsed and re-emitted as JSON. Preferred by
    SDK generators (`openapi-generator`, `oapi-codegen`).

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[GetOpenAPISpecJSONResponse200]
    """

    kwargs = _get_kwargs()

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
) -> GetOpenAPISpecJSONResponse200 | None:
    """This spec, as JSON.

     Same spec, YAML parsed and re-emitted as JSON. Preferred by
    SDK generators (`openapi-generator`, `oapi-codegen`).

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        GetOpenAPISpecJSONResponse200
    """

    return (
        await asyncio_detailed(
            client=client,
        )
    ).parsed
