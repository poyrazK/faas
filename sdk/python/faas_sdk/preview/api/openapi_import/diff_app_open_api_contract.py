from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.open_api_contract_diff_response import OpenAPIContractDiffResponse
from ...models.problem import Problem
from ...types import UNSET, Response, Unset


def _get_kwargs(
    slug: str,
    *,
    scope: str | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    params["scope"] = scope

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/v1/apps/{slug}/openapi/diff".format(
            slug=quote(str(slug), safe=""),
        ),
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Any | OpenAPIContractDiffResponse | Problem | None:
    if response.status_code == 200:
        response_200 = OpenAPIContractDiffResponse.from_dict(response.json())

        return response_200

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 503:
        response_503 = cast(Any, None)
        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Any | OpenAPIContractDiffResponse | Problem]:
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
    scope: str | Unset = UNSET,
) -> Response[Any | OpenAPIContractDiffResponse | Problem]:
    """Preview the production OpenAPI contract gate.

     Read-only ADR-121 contract diff. Compares the current projected
    OpenAPI surface against the latest captured live snapshot in the
    requested scope (default `prod`). `blocking=true` means a production
    promotion would be rejected while `FAAS_API_CONTRACT_DIFF_ENABLED`
    is enabled. The route remains registered while the flag is off and
    returns 503 `api_contract_diff_disabled`.

    Args:
        slug (str):
        scope (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | OpenAPIContractDiffResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        scope=scope,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    scope: str | Unset = UNSET,
) -> Any | OpenAPIContractDiffResponse | Problem | None:
    """Preview the production OpenAPI contract gate.

     Read-only ADR-121 contract diff. Compares the current projected
    OpenAPI surface against the latest captured live snapshot in the
    requested scope (default `prod`). `blocking=true` means a production
    promotion would be rejected while `FAAS_API_CONTRACT_DIFF_ENABLED`
    is enabled. The route remains registered while the flag is off and
    returns 503 `api_contract_diff_disabled`.

    Args:
        slug (str):
        scope (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | OpenAPIContractDiffResponse | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
        scope=scope,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    scope: str | Unset = UNSET,
) -> Response[Any | OpenAPIContractDiffResponse | Problem]:
    """Preview the production OpenAPI contract gate.

     Read-only ADR-121 contract diff. Compares the current projected
    OpenAPI surface against the latest captured live snapshot in the
    requested scope (default `prod`). `blocking=true` means a production
    promotion would be rejected while `FAAS_API_CONTRACT_DIFF_ENABLED`
    is enabled. The route remains registered while the flag is off and
    returns 503 `api_contract_diff_disabled`.

    Args:
        slug (str):
        scope (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | OpenAPIContractDiffResponse | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        scope=scope,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    scope: str | Unset = UNSET,
) -> Any | OpenAPIContractDiffResponse | Problem | None:
    """Preview the production OpenAPI contract gate.

     Read-only ADR-121 contract diff. Compares the current projected
    OpenAPI surface against the latest captured live snapshot in the
    requested scope (default `prod`). `blocking=true` means a production
    promotion would be rejected while `FAAS_API_CONTRACT_DIFF_ENABLED`
    is enabled. The route remains registered while the flag is off and
    returns 503 `api_contract_diff_disabled`.

    Args:
        slug (str):
        scope (str | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | OpenAPIContractDiffResponse | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            scope=scope,
        )
    ).parsed
