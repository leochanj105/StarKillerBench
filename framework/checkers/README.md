# Checkers

One executable per primitive. Each checker:

- Reads arguments positionally.
- Performs one check.
- Exits 0 on success (silent or one-line), non-zero on failure with a one-line reason.
- Runs from `REPO_ROOT` (the parent of `framework/`).

Invoked from `steps.yaml` as `<name> <args>` inside a `checks:` list.

Concrete checkers are added as the project needs them.
