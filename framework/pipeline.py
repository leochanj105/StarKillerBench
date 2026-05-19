#!/usr/bin/env python3
"""
pipeline.py — agent-driven build pipeline runner.

Reads <component-dir>/steps.yaml and runs each step in order. Two kinds:

  * Agent step (no `do:`) — renders framework/prompts/<id>.md.tmpl with
    file-level `vars:`, writes it to <component-dir>/prompts/<id>.md, then
    invokes claude inside a bwrap sandbox with permissions from the step's
    `permission:` profile (framework/permissions/<name>.yaml). Retries up
    to AGENT_MAX_ATTEMPTS times on failure; each retry appends the
    previous attempt's log tail to the prompt as `## Previous attempt
    failed`.

  * Shell step (`do:` set) — runs the shell command. No retry, no sandbox
    (shell steps are deterministic and authored by humans).

After `do`, every `check:` runs as `framework/checkers/<name> <args>` from
REPO_ROOT. Any non-zero exit fails the step and halts the pipeline.

State is recorded in <component-dir>/.state.yaml as `<id>: passed|failed`.
To redo a step: delete its line. To redo everything: delete the file.

Usage:
    python3 pipeline.py <component-dir>
"""

import os, subprocess, sys, yaml
from pathlib import Path

FW = Path(__file__).resolve().parent
ROOT = FW.parent
AGENT_MAX_ATTEMPTS = 3
HOME = os.environ.get("HOME", "/root")


def shell(cmd: str, log_path: Path) -> bool:
    """Run `cmd` in REPO_ROOT; append all output to `log_path`. True on exit 0."""
    with log_path.open("a") as log:
        log.write(f"\n$ {cmd}\n"); log.flush()
        return subprocess.run(cmd, shell=True, cwd=ROOT,
                              stdout=log, stderr=subprocess.STDOUT).returncode == 0


def render(tmpl: Path, out: Path, vars_: dict, append_failure: str | None = None) -> None:
    """Substitute {{KEY}} placeholders into `tmpl`, write to `out`.
    If `append_failure` is set, append it as a 'Previous attempt failed' block."""
    out.parent.mkdir(parents=True, exist_ok=True)
    text = tmpl.read_text()
    for k, v in vars_.items():
        text = text.replace(f"{{{{{k}}}}}", str(v))
    if append_failure:
        text += f"\n\n## Previous attempt failed\n\n```\n{append_failure}\n```\n"
    out.write_text(text)


def tail(path: Path, n: int = 50) -> str:
    """Last n lines of a file (empty if missing)."""
    if not path.exists():
        return ""
    return "\n".join(path.read_text().splitlines()[-n:])


def run_checks(step: dict, log_path: Path) -> bool:
    """Run each `check:` in order. True if all pass; False on first failure."""
    for check in step.get("checks") or []:
        if not shell(f"{FW}/checkers/{check}", log_path):
            return False
    return True


def load_profile(name: str) -> dict:
    """Load framework/permissions/<name>.yaml. Raises if missing."""
    path = FW / "permissions" / f"{name}.yaml"
    if not path.exists():
        sys.exit(f"unknown permission profile: {name} (no {path})")
    return yaml.safe_load(path.read_text()) or {}


def profile_to_allowed_tools(profile: dict) -> str:
    """Build the comma-separated string for claude --allowed-tools.
    Plain tools go in verbatim; bash patterns become `Bash(<pat>)`."""
    parts = list(profile.get("tools") or [])
    parts += [f"Bash({p})" for p in profile.get("bash") or []]
    return ",".join(parts)


def build_agent_cmd(prompt_path: Path, profile: dict) -> str:
    """Wrap `claude -p` in a bwrap sandbox: REPO_ROOT is the only read-write
    path the agent can see; system dirs are read-only; everything else
    (home, /var, /root, ...) is invisible."""
    tools = profile_to_allowed_tools(profile)
    bwrap = (
        "bwrap "
        "--ro-bind /usr /usr "
        "--ro-bind /etc /etc "
        "--ro-bind /lib /lib --ro-bind /lib64 /lib64 "
        "--ro-bind /bin /bin --ro-bind /opt /opt "
        f"--ro-bind {HOME}/.claude {HOME}/.claude "
        f"--bind {ROOT} {ROOT} "
        "--proc /proc --dev /dev --tmpfs /tmp "
        "--unshare-pid"
    )
    return f'{bwrap} claude -p --allowed-tools "{tools}" < "{prompt_path}"'


def run_agent_step(step: dict, comp_dir: Path, vars_: dict, log_path: Path) -> bool:
    """Render the prompt and invoke claude. Retry on failure, appending the
    previous attempt's log tail to each retry's prompt."""
    sid = step["id"]
    tmpl = FW / "prompts" / f"{sid}.md.tmpl"
    if not tmpl.exists():
        log_path.open("a").write(f"\nno template at {tmpl}\n")
        return False
    out = comp_dir / "prompts" / f"{sid}.md"
    profile = load_profile(step.get("permission") or "default")
    prev_fail: str | None = None

    for attempt in range(1, AGENT_MAX_ATTEMPTS + 1):
        log_path.open("a").write(f"\n--- attempt {attempt} ---\n")
        render(tmpl, out, vars_, append_failure=prev_fail)
        cmd = build_agent_cmd(out, profile)
        if shell(cmd, log_path) and run_checks(step, log_path):
            return True
        prev_fail = tail(log_path, 50)

    return False


def main() -> None:
    if len(sys.argv) != 2:
        sys.exit("usage: pipeline.py <component-dir>")
    comp = Path(sys.argv[1])
    pipe = yaml.safe_load((comp / "steps.yaml").read_text())
    state_p = comp / ".state.yaml"
    state = (yaml.safe_load(state_p.read_text()) if state_p.exists() else {}) or {}
    state.setdefault("service", pipe.get("service", comp.name))
    state.setdefault("steps", {})
    vars_ = pipe.get("vars") or {}

    os.environ["FRAMEWORK_DIR"] = str(FW)
    os.environ["REPO_ROOT"] = str(ROOT)

    for step in pipe.get("steps") or []:
        sid = step["id"]
        if state["steps"].get(sid) == "passed":
            print(f"↷ {sid}")
            continue

        print(f"▶ {sid}")
        log_path = comp / "logs" / f"{sid}.log"
        log_path.parent.mkdir(parents=True, exist_ok=True)

        if "do" in step:
            ok = shell(step["do"], log_path) and run_checks(step, log_path)
        else:
            ok = run_agent_step(step, comp, vars_, log_path)

        state["steps"][sid] = "passed" if ok else "failed"
        state_p.write_text(yaml.safe_dump(state, sort_keys=False))
        if not ok:
            sys.exit(f"✗ {sid}  (see {log_path})")
        print(f"✓ {sid}")

    print(f"✓ done: {state['service']}")


if __name__ == "__main__":
    main()
