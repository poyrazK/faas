"""Private fixture dispatch; authenticated application routes select principals."""

from query_backend import csv_queries


async def execute(request):
    _, _report, adapters = csv_queries()
    if request["action"] == "query-read":
        return await adapters["failure"].read_resource(
            request["uri"], request["access"], timeout=10
        )
    return await adapters[request["kind"]].call(
        request["name"], request["arguments"], request["access"], timeout=10
    )
