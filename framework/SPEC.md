# Framework Spec

A **pipeline** builds one component as a sequence of steps. Steps run in order. Each step ends as `passed` or `failed`. Re-running the pipeline skips `passed` steps.

## Files

For every component:

```
<component-dir>/
├── steps.yaml              # the pipeline (you write this)
├── .state.yaml             # progress (runner writes this)
├── logs/<step-id>.log      # per-step output stream
└── prompts/<step-id>.md    # rendered agent prompts (agent steps only)
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
steps:
  - id: <snake_case>     # unique. For agent steps, also the prompt template name.
    do: <shell-command>  # optional. Absent → agent step. Present → shell step.
    permission: <name>   # agent steps only; default "default".
                         # References framework/permissions/<name>.yaml.
    checks:              # optional. Each entry: "<checker-name> <args...>"
      - <checker> <args>
```

## Step kinds

| Kind | Trigger | Runner behavior |
|---|---|---|
| **shell** | `do:` is set | Runs `do:` as a shell command from `REPO_ROOT`. No sandbox, no retry. Use for deterministic work authored by humans (mkdir, protoc, docker build). |
| **agent** | `do:` is absent | (1) Renders `framework/prompts/<id>.md.tmpl` with `vars:` → `<component-dir>/prompts/<id>.md`. (2) Loads the `permission:` profile. (3) Invokes `claude -p` inside a `bwrap` sandbox with that profile's tools. (4) Retries up to 3 times on failure; each retry appends the previous log tail to the prompt as `## Previous attempt failed`. |

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

## Sandboxing

Agent steps run inside `bwrap` with:

- `<repo-root>` bind-mounted read-write.
- `/usr`, `/etc`, `/lib`, `/lib64`, `/bin`, `/opt`, `~/.claude` bind-mounted read-only.
- `/proc`, `/dev`, `tmpfs:/tmp`, fresh pid namespace.
- Network: on (so `go mod` works). Add `--unshare-net` in `pipeline.py` if you want airgapped runs.

The agent literally cannot see anything outside the bind-mounts — including your home, `/root`, `/var`, etc. Bash patterns guard against accidents inside the sandbox; bwrap guards against anything reaching outside.

## `.state.yaml`

```yaml
service: <name>
steps:
  scaffold_directories: passed
  write_grpc_proto: failed
  # missing entry = not yet run
```

To redo one step: delete its line. To redo everything: delete the file.

## Extension points

- **New checker.** Drop an executable in `framework/checkers/<name>`.
- **New prompt.** Drop `<id>.md.tmpl` in `framework/prompts/`.
- **New permission profile.** Drop `<name>.yaml` in `framework/permissions/`.
- **Different component layout.** `pipeline.py` is component-agnostic; point it at any directory matching the spec.

## Conventions

- Step IDs are meaningful snake_case names.
- Templates use `{{KEY}}` placeholders; unknown placeholders are left as-is so audits catch typos.
- Each agent step picks the smallest permission profile that works. README-writing steps don't need `Bash`; proto-writing doesn't either.
