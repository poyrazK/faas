# Contributing

This repository is published from Gregale's platform repository, where the
SDK is developed and tested together with the API it calls. Every commit here
names the upstream commit it came from in an `Upstream-Commit:` trailer.

- **Issues** are welcome here: bugs, missing API coverage, documentation.
- **Pull requests** are welcome too. We port an accepted change upstream and
  it comes back in the next sync, so the pull request itself is closed rather
  than merged. You keep the credit in the upstream commit.
- **Security issues** go to `security@gregale.dev`, never a public issue
  (see [SECURITY.md](SECURITY.md)).

Some tests in this repository read shared fixtures that live upstream, so a
local test run can skip or fail on them; the full suite runs upstream on every
change.
