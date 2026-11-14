from http import HTTPStatus
from typing import Any

import httpx

from ... import errors
from ...client import AuthenticatedClient, Client
from ...models.domain_manifest import DomainManifest
from ...models.domain_plan import DomainPlan
from ...models.problem import Problem
from ...types import Response


def _get_kwargs(
    *,
    body: DomainManifest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/v1/domains/plan",
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> DomainPlan | Problem | None:
    if response.status_code == 200:
        response_200 = DomainPlan.from_dict(response.json())

        return response_200

    if response.status_code == 400:
        response_400 = Problem.from_dict(response.json())

        return response_400

    if response.status_code == 401:
        response_401 = Problem.from_dict(response.json())

        return response_401

    if response.status_code == 403:
        response_403 = Problem.from_dict(response.json())

        return response_403

    if response.status_code == 404:
        response_404 = Problem.from_dict(response.json())

        return response_404

    if response.status_code == 409:
        response_409 = Problem.from_dict(response.json())

        return response_409

    if response.status_code == 429:
        response_429 = Problem.from_dict(response.json())

        return response_429

    if response.status_code == 503:
        response_503 = Problem.from_dict(response.json())

        return response_503

    if client.raise_on_unexpected_status:
        raise errors.UnexpectedStatus(response.status_code, response.content)
    else:
        return None


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[DomainPlan | Problem]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: DomainManifest,
) -> Response[DomainPlan | Problem]:
    """Review a declarative set of domain bindings.

     Reads one consistent snapshot without DNS probes or writes. Lists creates,
    moves and unchanged bindings, serving destination deployments, and default
    selections that a move clears. Omitted domains remain attached. All apps
    must share an account namespace and organization and the caller needs
    deployment-write access to every source and destination. A move requires
    verified ownership and a serving destination; it does not smoke-test the
    application. Traffic publication is validated again during atomic apply.

    Args:
        body (DomainManifest): Versioned desired bindings. Only listed domains change; omission
            never removes a binding. At most 20 bindings in a 128 KiB request. Use a separate file
            from the application deployment manifest.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[DomainPlan | Problem]
    """

    kwargs = _get_kwargs(
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
    body: DomainManifest,
) -> DomainPlan | Problem | None:
    """Review a declarative set of domain bindings.

     Reads one consistent snapshot without DNS probes or writes. Lists creates,
    moves and unchanged bindings, serving destination deployments, and default
    selections that a move clears. Omitted domains remain attached. All apps
    must share an account namespace and organization and the caller needs
    deployment-write access to every source and destination. A move requires
    verified ownership and a serving destination; it does not smoke-test the
    application. Traffic publication is validated again during atomic apply.

    Args:
        body (DomainManifest): Versioned desired bindings. Only listed domains change; omission
            never removes a binding. At most 20 bindings in a 128 KiB request. Use a separate file
            from the application deployment manifest.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        DomainPlan | Problem
    """

    return sync_detailed(
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: DomainManifest,
) -> Response[DomainPlan | Problem]:
    """Review a declarative set of domain bindings.

     Reads one consistent snapshot without DNS probes or writes. Lists creates,
    moves and unchanged bindings, serving destination deployments, and default
    selections that a move clears. Omitted domains remain attached. All apps
    must share an account namespace and organization and the caller needs
    deployment-write access to every source and destination. A move requires
    verified ownership and a serving destination; it does not smoke-test the
    application. Traffic publication is validated again during atomic apply.

    Args:
        body (DomainManifest): Versioned desired bindings. Only listed domains change; omission
            never removes a binding. At most 20 bindings in a 128 KiB request. Use a separate file
            from the application deployment manifest.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[DomainPlan | Problem]
    """

    kwargs = _get_kwargs(
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    body: DomainManifest,
) -> DomainPlan | Problem | None:
    """Review a declarative set of domain bindings.

     Reads one consistent snapshot without DNS probes or writes. Lists creates,
    moves and unchanged bindings, serving destination deployments, and default
    selections that a move clears. Omitted domains remain attached. All apps
    must share an account namespace and organization and the caller needs
    deployment-write access to every source and destination. A move requires
    verified ownership and a serving destination; it does not smoke-test the
    application. Traffic publication is validated again during atomic apply.

    Args:
        body (DomainManifest): Versioned desired bindings. Only listed domains change; omission
            never removes a binding. At most 20 bindings in a 128 KiB request. Use a separate file
            from the application deployment manifest.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        DomainPlan | Problem
    """

    return (
        await asyncio_detailed(
            client=client,
            body=body,
        )
    ).parsed
