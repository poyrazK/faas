# ADR-20261118170000 SDK pipeline Task manifests and bindings

Node and Python pipeline Task hosts can publish a scoped manifest at a
fixed MCP resource URI. It draws contract metadata from the same mounted
adapters as the Task operations. Start, status, and result scopes govern
visibility. Clarification and artifact scopes govern the corresponding
descriptors. Each exported contract and the scoped manifest have portable
digests.

Consumers fetch the manifest through authenticated MCP and generate a
standalone module for a selected pipeline using the portable job type
renderer. Modules contain typed inputs, outputs, replies, and artifact
names, and construct the existing preview Task client. CI can regenerate
against a refreshed manifest and compare the output with the saved binding.
