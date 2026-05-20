# Framework Spec

A **pipeline** builds one component as a sequence of steps. Steps run in order. Each step ends as `passed` or `failed`. Re-running the pipeline skips `passed` steps.

## Files

For every component:

```
<component-dir>/
├── steps.yaml                          # the pipeline (you write this)
├── SPEC.yaml                           # agent-facing service spec (you write this; YAML)
├── <agent-written files>               # the service code itself
└── .runner/                            # framework-managed; agent never touches
    ├── state.yaml                      # step status
    ├── logs/<step-id>.log              # per-step stdout/stderr
    └── prompts/
        ├── <step-id>.md                # rendered agent prompts (audit)
        └── <step-id>.context.md        # concat'd context per agent step (audit)
```

Provided by the framework:

```
framework/
├── pipeline.py                 # the runner
├── checkers/<name>             # one executable per check primitive
├── prompts/<step-id>.md.tmpl   # reusable prompt templates with {{KEY}} placeholders
└── permissions/<name>.yaml     # reusable permission profiles
```

At the repo root: `.claude/settings.json` — global Claude Code denies (e.g. `~/.ssh`, `/etc/shadow`). Applies to every invocation regardless of profile.

Everything is visible on disk. No hidden state.

## `steps.yaml`

```yaml
service: <name>          # informational
vars:                    # optional. Substituted into agent prompt templates.
  KEY: value
scope:                   # optional. Path globs the agent's Read/Write/Edit
  - services/<name>/**   # tools are confined to. Plus each step's `context:`
  - contracts/<name>/**  # files (Read only).
                         #
                         # The runner auto-denies access to framework files
                         # (steps.yaml, SPEC.md write, .runner/**) regardless
                         # of what's listed here — you don't have to carve
                         # them out manually.
steps:
  - id: <snake_case>     # unique. For agent steps, also the prompt template name.
    do: <shell-command>  # optional. Absent → agent step. Present → shell step.
    permission: <name>   # agent steps only; default "default".
                         # References framework/permissions/<name>.yaml.
    context:             # agent steps only; optional. List of file paths
      - FEATURES.md      # (relative to REPO_ROOT). Contents are concat'd into
      - ARCHITECTURE.md  # claude's --append-system-prompt at invocation time.
                         # Also persisted to <component>/prompts/<id>.context.md
                         # as the audit artifact.
    checks:              # optional. Each entry: "<checker-name> <args...>"
      - <checker> <args>
```

## Step kinds

| Kind | Trigger | Runner behavior |
|---|---|---|
| **shell** | `do:` is set | Runs `do:` as a shell command from `REPO_ROOT`. No sandbox, no retry. Use for deterministic work authored by humans (mkdir, protoc, docker build). |
| **agent** | `do:` is absent | (1) Renders `framework/prompts/<id>.md.tmpl` with `vars:` → `<component-dir>/prompts/<id>.md`. (2) Concatenates each file listed in `context:` into `<component-dir>/prompts/<id>.context.md`. (3) Loads the `permission:` profile. (4) Invokes `claude -p` inside a `bwrap` sandbox with that profile's tools, feeding the context file via `--append-system-prompt "$(cat ...)"`. (5) Retries up to 3 times on failure; each retry appends the previous log tail to the prompt as `## Previous attempt failed`. |

After `do` (either kind), every `check:` runs as `framework/checkers/<name> <args>` from `REPO_ROOT`. Any non-zero exit fails the step.

## Permission profiles

A profile names a set of tools the agent may use:

```yaml
# framework/permissions/<name>.yaml
tools: [Read, Write, Edit]    # plain Claude Code tools, no patterns
bash:                          # specific Bash command patterns
  - "go build:*"
  - "go test:*"
```

The runner combines these into `--allowed-tools "Read,Write,Edit,Bash(go build:*),Bash(go test:*)"`.

- Omit `tools:` and an empty `bash:` ⇒ the agent gets no tools (probably useless).
- Include `Bash` in `tools:` (no patterns) ⇒ full bash access. Avoid unless deliberate.

## Sandboxing (two layers)

**Layer 1 — bwrap (hard boundary).** Agent steps run inside `bwrap` with:

- `<repo-root>` bind-mounted read-write.
- `/usr`, `/etc`, `/lib`, `/lib64`, `/bin`, `/opt`, `~/.claude` bind-mounted read-only.
- `/proc`, `/dev`, `tmpfs:/tmp`, fresh pid namespace.
- Network: on (so `go mod` works). Add `--unshare-net` in `pipeline.py` if you want airgapped runs.

The agent literally cannot see anything outside the bind-mounts — home, `/root`, `/var`, etc. are invisible.

**Layer 2 — Read/Write/Edit path patterns (soft, intent-level).** The runner converts the step's permission profile into `--allowed-tools` patterns scoped to the file-level `scope:` globs plus each step's `context:` files. So even though FEATURES.md is bind-mounted into the sandbox at `/<repo>/FEATURES.md`, a `Read(FEATURES.md)` tool call by the agent is denied.

Caveat: `Bash` is not path-scoped — once it's in the allow-list, the agent can run `cat`, `grep`, etc. on any path the bwrap bind-mount makes visible. Bash patterns (`Bash(go build:*)` etc.) help, but a fully sandbox-tight design would need to prune the bwrap binds further.

## `.state.yaml`

```yaml
service: <name>
steps:
  scaffold_directories: passed
  write_grpc_proto: failed
  # missing entry = not yet run
```

To redo one step: delete its line. To redo everything: delete the file.

## Derived checks (no double-declaration)

Checks should **derive** from the SPEC, not enumerate alongside it. A check that lists the same RPC names that already live in `SPEC.md` will drift; one that parses the SPEC and validates the artifact against it won't.

Example — `framework/checkers/file_organization_matches_spec`:
```
file_organization_matches_spec <spec.yaml>
```
Reads `file_organization` from the spec, derives the expected directory layout, and verifies it on disk. Adding a directory to SPEC automatically tightens the check; removing one automatically loosens it.

Counter-example — don't write checkers that **duplicate what tests already verify**. A structural check that the proto declares the right RPCs is redundant: the agent's unit tests (driven by the same spec) call those RPCs, and `go_builds` + `go_tests_pass` fail compilation if any RPC is missing or misnamed. Prefer one strong behavioral check (the test runs) over many weak structural checks (the proto has the right text).

When writing a new checker, prefer this shape: take the source-of-truth doc plus the artifact, and verify the artifact agrees with the doc. Avoid checkers that hardcode names from the SPEC.

## Per-component `SPEC.yaml`

Every component has a `SPEC.yaml` alongside its `steps.yaml`. This is the agent-facing spec — YAML, not markdown, so checkers and other agents parse it unambiguously.

Top-level keys (all required):

1. `summary` — one paragraph.
2. `interface` — `{ protocol, service, package, rpcs: [...] }`.
3. `error_semantics` — list of `{ rpc, condition, code }` entries.
4. `state` — `{ kind, description }`.
5. `dependent_interface` — list of `{ service, rpc, purpose }` entries, or `[]`.

A copyable template lives at `framework/spec.template.yaml`. The full schema is documented in `framework/spec.format.md`, which the runner **automatically prepends** to every agent step's context so the agent can interpret the spec consistently.

The SPEC.yaml is the **single source of truth** for what the service currently is. It describes *what*, not *how* — implementation conventions (Go version, port numbers, Dockerfile pattern, Makefile targets) belong in prompt templates. Deferred features are not in SPEC; they're a prompt-level concern.

## One big step + many checkers

Rather than fanning the implementation out across many small agent steps (`write_proto` → `implement_handlers` → `write_tests` → ...), services typically use a single `implement_service` agent step driven by the SPEC, followed by checkers that validate each piece in parallel. The agent has the full SPEC in context and produces a coherent implementation in one pass; the checkers — `spec_matches_proto`, `go_builds`, `go_tests_pass`, `readme_sections_present`, etc. — catch any drift from the SPEC after the fact.

When a check fails, the runner retries the step (up to 3 attempts) with the previous attempt's failed checker output appended to the prompt as `## Previous attempt failed`. The agent sees what went wrong and tries again.

## Extension points

- **New checker.** Drop an executable in `framework/checkers/<name>`.
- **New prompt.** Drop `<id>.md.tmpl` in `framework/prompts/`.
- **New permission profile.** Drop `<name>.yaml` in `framework/permissions/`.
- **New component.** Copy `framework/spec.template.md` to `<component>/SPEC.md` and fill it in; write the matching `<component>/steps.yaml`.
- **Different component layout.** `pipeline.py` is component-agnostic; point it at any directory matching the spec.

## Conventions

- Step IDs are meaningful snake_case names.
- Templates use `{{KEY}}` placeholders; unknown placeholders are left as-is so audits catch typos.
- Each agent step picks the smallest permission profile that works. README-writing steps don't need `Bash`; proto-writing doesn't either.
