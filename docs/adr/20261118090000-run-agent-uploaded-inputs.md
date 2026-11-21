# Scoped uploaded inputs for Agent Run tasks

**Status** Accepted for the Run preview

A hosted Agent needs to process customer files through the same immutable tasks
it uses for JSON. Giving the app an account Runs key would let it read unrelated
files and request other work. Passing bytes through RAP would exceed the tool
envelope for typical report datasets.

A trusted backend with account deploy authority and access to the endpoint's app
can issue a grant for a ready upload and an active task version delegated to that
endpoint. The account, endpoint, thread, logical tool call, task name and version
authenticate the token as associated data. The encrypted grant carries the file
identity, upload generation, guest path and expiration. The deployment's shared
session binding secret derives a separate AEAD key. Requires the existing live
app credential and tool-result callback when invoking. Shared organization
permission for issuance uses the same application security action as callbacks.

The admission transaction looks up the durable logical tool call before opening
a grant. A lost acknowledgement can recover the original Run after revocation,
expiry or reader restart. A change to the submitted grant or arguments conflicts
with the original admission. Conflict responses leave the original tool result
slot available to its admitted Run. New admissions verify the upload generation
before common Run staging seals the guest input metadata. File revocation and
execution settlement retain their existing transactional file-lease behavior.

The token has a separate format from artifact shares and callback URLs. It carries
authority to stage one uploaded file at a fixed path. The initial surface is
`gregale_task` with up to four grants and the existing Run file and RAP limits.
The client persists the exact grant alongside its private pending dispatch.
