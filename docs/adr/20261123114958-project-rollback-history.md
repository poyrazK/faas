# Discover project rollback receipts

Status: Accepted

`gregale project rollback <slug> --history` reads recent receipts from the project. The list puts the running operation first and shows up to 20 entries, with the remaining receipts newest first. Each entry includes its operation ID, state, completed cutovers, and linked undo or source rollback when applicable.

The list uses project read access and the same project ownership boundary as receipt lookup. A start conflict identifies the running receipt and shows the command to inspect it. Operators can recover the receipt from a fresh CLI session after a lost response.
