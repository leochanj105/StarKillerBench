# Framework

A small Python harness that builds one component as a sequence of agent + shell steps.

- **Spec**: [`SPEC.md`](SPEC.md) — file layout, schema, conventions.
- **Runner**: [`pipeline.py`](pipeline.py) — `python3 pipeline.py <component-dir>`.

## Requirements

- Python 3.10+
- PyYAML
- `claude` CLI on PATH (for agent steps)
- Whatever a step's `do:` or its checkers need (Go, Docker, etc.)
