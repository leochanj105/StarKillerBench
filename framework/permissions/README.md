# Permission profiles

One YAML file per profile. Agent steps reference a profile by name via `permission:` in `steps.yaml`. The runner converts the profile into a `--allowed-tools` string for `claude -p`.

## Format

```yaml
tools: [Read, Write, Edit]   # plain Claude Code tools (no patterns)
bash:                         # specific Bash command patterns
  - "go build:*"
  - "go test:*"
```

The runner produces: `--allowed-tools "Read,Write,Edit,Bash(go build:*),Bash(go test:*)"`.

## Naming

Profiles are named for what they enable, not what step uses them — they should be reusable. Good: `go_dev`, `edit_only`, `readme_writer`. Bad: `write_grpc_proto_perms`.

## Defense in depth

`bwrap` (configured in `pipeline.py`) is the real boundary — it restricts what filesystem paths the agent can see at all. Profiles narrow what the agent does **within** the sandbox. Both layers matter:

- Without profiles: agent has full Bash inside the sandbox, can break things within `REPO_ROOT`.
- Without bwrap: agent's Bash can escape Claude Code's path patterns and reach anywhere your user can.

(Concrete profiles added as needed.)
