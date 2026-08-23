from http import HTTPStatus
from typing import Any, cast
from urllib.parse import quote

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.import_app_open_api_body import ImportAppOpenAPIBody
from ...models.import_app_open_api_response_200 import ImportAppOpenAPIResponse200
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    slug: str,
    *,
    body: ImportAppOpenAPIBody,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/apps/{slug}/openapi".format(
            slug=quote(str(slug), safe=""),
        ),
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Any | ImportAppOpenAPIResponse200 | Problem | None:
    if response.status_code == 200:
        response_200 = ImportAppOpenAPIResponse200.from_dict(response.json())

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

    if response.status_code == 413:
        response_413 = cast(Any, None)
        return response_413

    if response.status_code == 422:
        response_422 = cast(Any, None)
        return response_422

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[Any | ImportAppOpenAPIResponse200 | Problem]:
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
    body: ImportAppOpenAPIBody,
) -> Response[Any | ImportAppOpenAPIResponse200 | Problem]:
    """Import an OpenAPI document for an app.

     Customer-facing import (ADR-126 / issue #975 item #2).
    Reads the body, validates via
    pkg/openapiimport.ValidateImport (structural-minimum
    OpenAPI 3.0 / 3.1 check), enforces size + endpoint
    caps, persists via UpsertAppOpenAPIDoc, emits
    app.openapi_import.replaced audit + pg_notify on
    NotifyAppOpenAPIDocChanged. The auto-gen cache
    (NotifyAppOpenAPIDocChanged + NotifyEdgeRuleChanged
    fan-in) is flushed per-app so the next `?source=auto`
    read recomputes.

    Limits (abuse-surface, not plan-tier): body cap
    state.OpenAPIImportMaxDocBytes (256 KiB), endpoint
    cap state.OpenAPIImportMaxEndpoints (50). Per-account
    row cap is Plan.OpenAPIImportsPerAccount.

    Args:
        slug (str):
        body (ImportAppOpenAPIBody): OpenAPI 3.0/3.1 document. The validator only requires
            openapi, info, paths to be present + object-shaped; everything else is passthrough.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | ImportAppOpenAPIResponse200 | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: ImportAppOpenAPIBody,
) -> Any | ImportAppOpenAPIResponse200 | Problem | None:
    """Import an OpenAPI document for an app.

     Customer-facing import (ADR-126 / issue #975 item #2).
    Reads the body, validates via
    pkg/openapiimport.ValidateImport (structural-minimum
    OpenAPI 3.0 / 3.1 check), enforces size + endpoint
    caps, persists via UpsertAppOpenAPIDoc, emits
    app.openapi_import.replaced audit + pg_notify on
    NotifyAppOpenAPIDocChanged. The auto-gen cache
    (NotifyAppOpenAPIDocChanged + NotifyEdgeRuleChanged
    fan-in) is flushed per-app so the next `?source=auto`
    read recomputes.

    Limits (abuse-surface, not plan-tier): body cap
    state.OpenAPIImportMaxDocBytes (256 KiB), endpoint
    cap state.OpenAPIImportMaxEndpoints (50). Per-account
    row cap is Plan.OpenAPIImportsPerAccount.

    Args:
        slug (str):
        body (ImportAppOpenAPIBody): OpenAPI 3.0/3.1 document. The validator only requires
            openapi, info, paths to be present + object-shaped; everything else is passthrough.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | ImportAppOpenAPIResponse200 | Problem
    """

    return sync_detailed(
        slug=slug,
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: ImportAppOpenAPIBody,
) -> Response[Any | ImportAppOpenAPIResponse200 | Problem]:
    """Import an OpenAPI document for an app.

     Customer-facing import (ADR-126 / issue #975 item #2).
    Reads the body, validates via
    pkg/openapiimport.ValidateImport (structural-minimum
    OpenAPI 3.0 / 3.1 check), enforces size + endpoint
    caps, persists via UpsertAppOpenAPIDoc, emits
    app.openapi_import.replaced audit + pg_notify on
    NotifyAppOpenAPIDocChanged. The auto-gen cache
    (NotifyAppOpenAPIDocChanged + NotifyEdgeRuleChanged
    fan-in) is flushed per-app so the next `?source=auto`
    read recomputes.

    Limits (abuse-surface, not plan-tier): body cap
    state.OpenAPIImportMaxDocBytes (256 KiB), endpoint
    cap state.OpenAPIImportMaxEndpoints (50). Per-account
    row cap is Plan.OpenAPIImportsPerAccount.

    Args:
        slug (str):
        body (ImportAppOpenAPIBody): OpenAPI 3.0/3.1 document. The validator only requires
            openapi, info, paths to be present + object-shaped; everything else is passthrough.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | ImportAppOpenAPIResponse200 | Problem]
    """

    kwargs = _get_kwargs(
        slug=slug,
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    slug: str,
    *,
    client: AuthenticatedClient | Client,
    body: ImportAppOpenAPIBody,
) -> Any | ImportAppOpenAPIResponse200 | Problem | None:
    """Import an OpenAPI document for an app.

     Customer-facing import (ADR-126 / issue #975 item #2).
    Reads the body, validates via
    pkg/openapiimport.ValidateImport (structural-minimum
    OpenAPI 3.0 / 3.1 check), enforces size + endpoint
    caps, persists via UpsertAppOpenAPIDoc, emits
    app.openapi_import.replaced audit + pg_notify on
    NotifyAppOpenAPIDocChanged. The auto-gen cache
    (NotifyAppOpenAPIDocChanged + NotifyEdgeRuleChanged
    fan-in) is flushed per-app so the next `?source=auto`
    read recomputes.

    Limits (abuse-surface, not plan-tier): body cap
    state.OpenAPIImportMaxDocBytes (256 KiB), endpoint
    cap state.OpenAPIImportMaxEndpoints (50). Per-account
    row cap is Plan.OpenAPIImportsPerAccount.

    Args:
        slug (str):
        body (ImportAppOpenAPIBody): OpenAPI 3.0/3.1 document. The validator only requires
            openapi, info, paths to be present + object-shaped; everything else is passthrough.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | ImportAppOpenAPIResponse200 | Problem
    """

    return (
        await asyncio_detailed(
            slug=slug,
            client=client,
            body=body,
        )
    ).parsed
