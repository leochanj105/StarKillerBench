#!/usr/bin/env python3
"""
pipeline.py — agent-driven build pipeline runner.

Reads <component-dir>/steps.yaml; for each step runs its `do` (a shell
command) or, when `do` is absent, renders <id>.md.tmpl and invokes
`claude -p` inside a bwrap sandbox. Runs `checks:` after `do`; any non-zero
exit halts the pipeline. Agent steps retry up to MAX_ATTEMPTS with the
previous attempt's log tail appended to the prompt.

State, logs, and rendered prompts live under <component-dir>/.runner/.

Usage: python3 pipeline.py <component-dir>
"""
import os, subprocess, sys, yaml
from pathlib import Path

FW = Path(__file__).resolve().parent
ROOT = FW.parent
HOME = os.environ.get("HOME", "/root")
RUNNER_DIR = ".runner"
MAX_ATTEMPTS = 3


def shell(cmd, log):
    """Run `cmd` in REPO_ROOT; append output to `log`. True on exit 0."""
    with log.open("a") as f:
        f.write(f"\n$ {cmd}\n"); f.flush()
        return subprocess.run(cmd, shell=True, cwd=ROOT,
                              stdout=f, stderr=subprocess.STDOUT).returncode == 0


def tail(path, n=50):
    return "\n".join(path.read_text().splitlines()[-n:]) if path.exists() else ""


def run_checks(step, log):
    for check in step.get("checks") or []:
        if not shell(f"{FW}/checkers/{check}", log):
            return False
    return True


# ---- agent step plumbing ----

def render_template(step, comp, vars_, prev_fail=None):
    """Substitute {{KEY}} into the template, append prev failure if any, write out."""
    sid = step["id"]
    out = comp / RUNNER_DIR / "prompts" / f"{sid}.md"
    out.parent.mkdir(parents=True, exist_ok=True)
    text = (FW / "prompts" / f"{sid}.md.tmpl").read_text()
    for k, v in vars_.items():
        text = text.replace(f"{{{{{k}}}}}", str(v))
    if prev_fail:
        text += f"\n\n## Previous attempt failed\n\n```\n{prev_fail}\n```\n"
    out.write_text(text)
    return out


def build_context(step, comp):
    """Concat spec.format.md + each `context:` file into <id>.context.md.
    Returns the path, or None when there are no context files."""
    files = step.get("context") or []
    if not files:
        return None
    out = comp / RUNNER_DIR / "prompts" / f"{step['id']}.context.md"
    out.parent.mkdir(parents=True, exist_ok=True)
    parts = []
    fmt = FW / "spec.format.md"
    if fmt.exists():
        parts.append(f"# (framework) spec.format.md\n\n{fmt.read_text()}")
    for f in files:
        p = ROOT / f
        parts.append(f"# {f}\n\n{p.read_text() if p.exists() else '(missing)'}")
    out.write_text("\n\n---\n\n".join(parts))
    return out


def _dependent_service_binds(comp):
    """For each name in SPEC.yaml's `dependent_interface`, ro-bind exactly
    three paths so the agent can import the upstream's Go package: its
    public `api/v1` package, plus `go.mod` and `go.sum` for Go module
    resolution. `dependent_interface` is a bare list of service names."""
    args = []
    spec_path = comp / "SPEC.yaml"
    if not spec_path.exists():
        return args
    spec = yaml.safe_load(spec_path.read_text()) or {}
    for svc in spec.get("dependent_interface") or []:
        for sub in ["api/v1", "go.mod", "go.sum"]:
            p = ROOT / "services" / svc / sub
            if p.exists():
                args += ["--ro-bind", str(p), str(p)]
    return args


def _bwrap_mounts(comp, scope):
    """Return the list of bwrap CLI args for the agent sandbox.

    bwrap takes flags like `--ro-bind SRC DEST`. We build them as a flat
    list of strings, in the order bwrap will see them. Order matters: a
    later mount on the same path overrides an earlier one — that's how we
    bind the workspace as read-write, then re-bind specific files inside
    it as read-only or empty.

    Read top-to-bottom for the complete file view the agent gets.
    """
    args = []

    # See, but can't edit: system files the agent's tools need.
    for d in ["/usr", "/etc", "/lib", "/lib64", "/bin"]:
        args += ["--ro-bind", d, d]
    # ~/.local: the `claude` binary lives at ~/.local/bin/claude (symlink to
    # ~/.local/share/claude/versions/<v>).
    args += ["--ro-bind", f"{HOME}/.local", f"{HOME}/.local"]
    # ~/.claude: claude's config dir + session state (rw — claude writes session logs).
    args += ["--bind", f"{HOME}/.claude", f"{HOME}/.claude"]
    # ~/.claude.json: claude's main config file (lives outside ~/.claude/).
    args += ["--bind", f"{HOME}/.claude.json", f"{HOME}/.claude.json"]
    # ~/go: read-write so `go mod tidy` can populate the module cache;
    # also contains Go's plugin bin (protoc-gen-go etc.).
    args += ["--bind", f"{HOME}/go", f"{HOME}/go"]

    # See AND edit: the agent's workspace, from steps.yaml `scope:`.
    for s in scope:
        p = ROOT / s.removesuffix("/**").rstrip("/")
        args += ["--bind", str(p), str(p)]

    # Read-only: each upstream service this component depends on. Exposes
    # just the public `api/v1` package and go.mod / go.sum so the agent can
    # import the upstream's types. The repo-level go.work makes the
    # workspace's modules resolvable without `replace` directives.
    args += _dependent_service_binds(comp)

    # Read-only: the repo's go.work, so the agent's `go mod tidy` sees the
    # workspace and resolves cross-service imports without trying to write
    # to upstream modules' go.mod files.
    gowork = ROOT / "go.work"
    if gowork.exists():
        args += ["--ro-bind", str(gowork), str(gowork)]

    # Re-bind specific files INSIDE the workspace to protect them.
    args += ["--tmpfs", str(comp / RUNNER_DIR)]                          # framework state → empty
    args += ["--ro-bind", "/dev/null", str(comp / "steps.yaml")]         # pipeline file → empty
    args += ["--ro-bind", str(comp / "SPEC.yaml"), str(comp / "SPEC.yaml")]  # spec → read-only
    for tf in sorted(Path(comp).rglob("*_test.go")):
        args += ["--ro-bind", str(tf), str(tf)]                          # audited tests → read-only

    # Functional minima — /proc, /dev, /tmp must exist or some tools refuse
    # to start. Not security; just so things don't crash on missing paths.
    args += ["--proc", "/proc", "--dev", "/dev", "--tmpfs", "/tmp"]

    return args


def _bwrap_cmd(comp, scope):
    """Assemble the `bwrap ...` shell-command prefix from the flat arg list."""
    return "bwrap " + " ".join(_bwrap_mounts(comp, scope))


def _claude_tools(profile):
    """The `--allowed-tools` value. Just the bare tool names plus any narrow
    Bash patterns from `profile.bash`. No path patterns — paths are bwrap's
    job, enforced at the kernel level."""
    tools = list(profile.get("tools") or [])
    tools += [f"Bash({p})" for p in profile.get("bash") or []]
    return ",".join(tools)


def claude_cmd(prompt, ctx, profile, scope, comp):
    """The full sandboxed `claude -p ...` shell command.

    Two layers:
        bwrap (filesystem visibility)   — see _bwrap_mounts
            └── --allowed-tools         — see _claude_tools (tool enablement only)

    The agent can only see paths that bwrap makes visible; within those, the
    enabled tools work without further restriction.
    """
    flags = f'--allowed-tools "{_claude_tools(profile)}" --verbose --output-format stream-json'
    if ctx is not None:
        # $(cat ...) lets the shell pass arbitrary file content as a single
        # argv element without us having to escape it ourselves.
        flags += f' --append-system-prompt "$(cat \'{ctx}\')"'
    return f'{_bwrap_cmd(comp, scope)} claude -p {flags} < "{prompt}"'


def show_sandbox(comp, scope):
    """Print the bwrap command, one flag-group per line, for audit."""
    args = _bwrap_mounts(comp, scope)
    print(f"# bwrap for {comp}:")
    print("bwrap \\")
    # Group bwrap args sensibly: --ro-bind/--bind take 2 path args; --proc/--dev/--tmpfs take 1.
    i = 0
    while i < len(args):
        flag = args[i]
        if flag in ("--ro-bind", "--bind"):
            print(f"  {flag} {args[i+1]} {args[i+2]}")
            i += 3
        elif flag in ("--proc", "--dev", "--tmpfs"):
            print(f"  {flag} {args[i+1]}")
            i += 2
        else:
            print(f"  {flag}")
            i += 1


def run_agent_step(step, comp, vars_, scope, log):
    profile_path = FW / "permissions" / f"{step.get('permission') or 'default'}.yaml"
    if not profile_path.exists():
        sys.exit(f"unknown permission profile: {profile_path.stem}")
    profile = yaml.safe_load(profile_path.read_text()) or {}
    ctx = build_context(step, comp)   # built once; same across retries

    prev_fail = None
    for attempt in range(1, MAX_ATTEMPTS + 1):
        log.open("a").write(f"\n--- attempt {attempt} ---\n")
        prompt = render_template(step, comp, vars_, prev_fail=prev_fail)
        if shell(claude_cmd(prompt, ctx, profile, scope, comp), log) \
                and run_checks(step, log):
            return True
        prev_fail = tail(log, 50)
    return False


# ---- main ----

def main():
    args = sys.argv[1:]
    if len(args) < 1:
        sys.exit("usage: pipeline.py <component-dir> [--show-sandbox]")
    # Resolve to an absolute path so every downstream operation (bwrap
    # mounts, log files, state file) has unambiguous paths.
    comp = (ROOT / Path(args[0])).resolve()
    pipe = yaml.safe_load((comp / "steps.yaml").read_text())
    scope = pipe.get("scope") or []

    # Audit helper: dump the bwrap mount table and exit.
    if "--show-sandbox" in args:
        show_sandbox(comp, scope)
        return

    state_p = comp / RUNNER_DIR / "state.yaml"
    state_p.parent.mkdir(parents=True, exist_ok=True)
    state = (yaml.safe_load(state_p.read_text()) if state_p.exists() else {}) or {}
    state.setdefault("service", pipe.get("service", comp.name))
    state.setdefault("steps", {})
    vars_ = pipe.get("vars") or {}

    os.environ["FRAMEWORK_DIR"] = str(FW)
    os.environ["REPO_ROOT"] = str(ROOT)
    # Prepend ~/go/bin so protoc finds its Go plugins (protoc-gen-go, etc.).
    os.environ["PATH"] = f"{HOME}/go/bin:" + os.environ.get("PATH", "")
    # Defensively clear proxy env vars that might point at a local proxy
    # (e.g., clash) which the sandbox can't reach.
    for k in ("HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY",
              "http_proxy", "https_proxy", "all_proxy"):
        os.environ.pop(k, None)

    for step in pipe.get("steps") or []:
        sid = step["id"]
        if state["steps"].get(sid) == "passed":
            print(f"↷ {sid}")
            continue
        print(f"▶ {sid}")
        log = comp / RUNNER_DIR / "logs" / f"{sid}.log"
        log.parent.mkdir(parents=True, exist_ok=True)
        if "do" in step:
            ok = shell(step["do"], log) and run_checks(step, log)
        else:
            ok = run_agent_step(step, comp, vars_, scope, log)
        state["steps"][sid] = "passed" if ok else "failed"
        state_p.write_text(yaml.safe_dump(state, sort_keys=False))
        if not ok:
            sys.exit(f"✗ {sid}  (see {log})")
        print(f"✓ {sid}")
    print(f"✓ done: {state['service']}")


if __name__ == "__main__":
    main()
