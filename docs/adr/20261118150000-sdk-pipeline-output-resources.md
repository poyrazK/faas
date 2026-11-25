# ADR-20261118150000 SDK pipeline output resources

Node and Python pipeline tool adapters can publish named UTF-8 artifact
resources from a validated successful output. A resource slot declares its
bucket, selection function, MIME type, and byte budget. The application
toolkit applies the registered resource scopes to links and reads. The same
tool result supplies links to both direct callers and completed MCP Tasks.

A link encodes the request ID and a digest over the resource slot, receipt
identity, exact run ID, and normalized artifact descriptor. The reader
resolves the existing receipt under current result authorization, verifies
the file with the managed artifact reader, and rechecks the receipt and
permission before disclosing bytes. A replica can regenerate the link from
the same receipt and output without an additional store.
