#!/usr/bin/env python3
"""Routing eval harness template (Phase 0.1, see ../routing-eval-protocol.md).

Python 3, standard library only. Two subcommands:

  run      run every prompt under every arm, k repetitions, and write one JSON
           line per run to <out>/runs.jsonl. Prints the commands and does
           nothing else unless --execute is given.
  analyze  read runs.jsonl and print the metrics, paired bootstrap confidence
           intervals and the verdict required by the protocol.

Nothing here touches Claude Code state: each run is `claude -p` in a scratch
working directory, one turn, and the tool_use event is read from the stream.
The harness never installs, enables or disables anything. An arm is a command
template, so the profile arm can be the launcher or a hand-made --settings
file; the harness does not care how the set was masked.

Prompt file (JSON lines), one object per line:
  {"id": "t1-001", "tier": 1, "prompt": "...",
   "expect": {"skills": ["pdf"]}}        tier 1 and 2: acceptable skill names
  {"id": "t3-001", "tier": 3, "prompt": "...",
   "expect": {"none": true}}             tier 3: no skill should fire
A skill name matches a tool_use skill when equal or when the tool_use skill is
"<plugin>:<name>" and the name part equals it. Use "plugin:" + name to require
a plugin ("plugin:acme-deploy" matches any skill of that plugin).

An arm is `--arm NAME=COMMAND`, split with shlex and run without a shell. The
prompt is piped to stdin, which `claude -p` reads, so it never appears on argv.
Placeholders {model}, {max_turns} and {prompt_file} are substituted in the
command. Example control arm:
  --arm control="claude -p --output-format stream-json --verbose --max-turns {max_turns} --model {model}"
The profile arm is the same command started through the launcher (or with a
generated --settings file); see the protocol for the exact pair.
"""
import argparse
import hashlib
import json
import os
import random
import shlex
import subprocess
import sys
import tempfile
import time

SKILL_TOOL_NAMES = {"Skill"}  # protocol step P0 verifies this against a real stream


def load_prompts(path):
    prompts = []
    with open(path, encoding="utf-8") as f:
        for n, line in enumerate(f, 1):
            line = line.strip()
            if not line or line.startswith("#"):
                continue
            try:
                p = json.loads(line)
            except json.JSONDecodeError as e:
                sys.exit(f"{path}:{n}: invalid JSON: {e}")
            for key in ("id", "tier", "prompt", "expect"):
                if key not in p:
                    sys.exit(f"{path}:{n}: missing key {key!r}")
            if p["tier"] not in (1, 2, 3):
                sys.exit(f"{path}:{n}: tier must be 1, 2 or 3")
            exp = p["expect"]
            if set(exp) - {"skills", "none"}:
                sys.exit(f"{path}:{n}: unknown expect keys {sorted(set(exp) - {'skills', 'none'})}")
            if not (exp.get("none") or exp.get("skills")):
                sys.exit(f"{path}:{n}: expect needs skills or none")
            prompts.append(p)
    ids = [p["id"] for p in prompts]
    if len(ids) != len(set(ids)):
        sys.exit("duplicate prompt ids")
    return prompts


def skill_matches(used, wanted):
    """True when the skill the model used satisfies one wanted name."""
    for w in wanted:
        if w.startswith("plugin:"):
            if used.split(":", 1)[0] == w[len("plugin:"):] and ":" in used:
                return True
        elif used == w or used.split(":", 1)[-1] == w:
            return True
    return False


def parse_stream(text):
    """Extract what the protocol measures from a stream-json transcript."""
    init = {}
    skills_used = []
    first_tool = None
    first_usage = None
    result = {}
    for line in text.splitlines():
        line = line.strip()
        if not line.startswith("{"):
            continue
        try:
            ev = json.loads(line)
        except json.JSONDecodeError:
            continue
        t = ev.get("type")
        if t == "system" and ev.get("subtype") == "init":
            init = {
                "plugins": len(ev.get("plugins") or []),
                "skills": len(ev.get("skills") or []),
                "slash_commands": len(ev.get("slash_commands") or []),
                "agents": len(ev.get("agents") or []),
                "tools": len(ev.get("tools") or []),
                "mcp_servers": len(ev.get("mcp_servers") or []),
                "model": ev.get("model"),
                "claude_code_version": ev.get("claude_code_version"),
            }
        elif t == "assistant":
            msg = ev.get("message") or {}
            if first_usage is None and msg.get("usage"):
                u = msg["usage"]
                first_usage = (u.get("input_tokens", 0) + u.get("cache_creation_input_tokens", 0)
                               + u.get("cache_read_input_tokens", 0))
            for block in msg.get("content") or []:
                if block.get("type") != "tool_use":
                    continue
                name = block.get("name")
                if first_tool is None:
                    first_tool = name
                if name in SKILL_TOOL_NAMES:
                    inp = block.get("input") or {}
                    skills_used.append(inp.get("skill") or inp.get("command") or "")
        elif t == "result":
            result = {"is_error": ev.get("is_error"), "cost_usd": ev.get("total_cost_usd"),
                      "num_turns": ev.get("num_turns")}
    return {"init": init, "skills_used": skills_used, "first_tool": first_tool,
            "context_tokens": first_usage, "result": result}


def score(prompt, parsed):
    """Per-run outcome flags. Only the first skill use counts as the choice."""
    exp = prompt["expect"]
    used = parsed["skills_used"]
    first = used[0] if used else None
    if exp.get("none"):
        correct = first is None
        wrong = first is not None
        missed = False
    else:
        correct = first is not None and skill_matches(first, exp["skills"])
        wrong = first is not None and not correct
        missed = first is None
    return {"correct": correct, "wrong": wrong, "missed": missed}


def cmd_run(a):
    prompts = load_prompts(a.prompts)
    arms = {}
    for spec in a.arm:
        name, _, cmd = spec.partition("=")
        if not name or not cmd:
            sys.exit(f"--arm must be NAME=COMMAND, got {spec!r}")
        arms[name] = shlex.split(cmd)
    if len(arms) < 2:
        sys.exit("need at least two arms (control and profile)")
    jobs = [(p, arm, rep) for p in prompts for arm in arms for rep in range(a.reps)]
    rng = random.Random(a.seed)
    rng.shuffle(jobs)  # interleave arms and prompts to spread drift and rate limits
    if a.max_runs and len(jobs) > a.max_runs:
        sys.exit(f"{len(jobs)} runs exceeds --max-runs {a.max_runs}; raise it deliberately")
    pfile_hash = hashlib.sha256(open(a.prompts, "rb").read()).hexdigest()
    print(f"prompts={len(prompts)} arms={list(arms)} reps={a.reps} runs={len(jobs)} "
          f"prompt_sha256={pfile_hash}", file=sys.stderr)
    if not a.execute:
        for i, (p, arm, rep) in enumerate(jobs, 1):
            argv = [x.format(prompt_file="<prompt_file>", model=a.model, max_turns=a.max_turns) for x in arms[arm]]
            print(f"[{i}/{len(jobs)}] DRY RUN {arm} {p['id']} rep={rep}: {shlex.join(argv)}")
        print("dry run only; pass --execute to run", file=sys.stderr)
        return
    scratch = tempfile.mkdtemp(prefix="routing-eval-")  # cwd for every run: no project settings
    os.makedirs(a.out, exist_ok=True)
    out_path = os.path.join(a.out, "runs.jsonl")
    for i, (p, arm, rep) in enumerate(jobs, 1):
        pf = os.path.join(scratch, "prompt.txt")
        with open(pf, "w", encoding="utf-8") as f:
            f.write(p["prompt"])
        argv = [x.format(prompt_file=pf, model=a.model, max_turns=a.max_turns) for x in arms[arm]]
        t0 = time.time()
        try:
            proc = subprocess.run(argv, input=p["prompt"], capture_output=True, text=True,
                                  cwd=scratch, timeout=a.timeout)
            stdout, code, err = proc.stdout, proc.returncode, proc.stderr[-500:]
        except subprocess.TimeoutExpired:
            stdout, code, err = "", -1, "timeout"
        parsed = parse_stream(stdout)
        rec = {"id": p["id"], "tier": p["tier"], "arm": arm, "rep": rep,
               "exit": code, "seconds": round(time.time() - t0, 1), "stderr_tail": err,
               "prompt_sha256": pfile_hash, **parsed, **score(p, parsed)}
        with open(out_path, "a", encoding="utf-8") as f:
            f.write(json.dumps(rec) + "\n")
        print(f"[{i}/{len(jobs)}] {arm} {p['id']} rep={rep} first={parsed['skills_used'][:1]} "
              f"correct={rec['correct']}", file=sys.stderr)


def mean(xs):
    return sum(xs) / len(xs) if xs else float("nan")


def bootstrap_ci(diffs, rng, n=10000):
    """95% percentile CI of the mean of per-prompt paired differences."""
    if not diffs:
        return (float("nan"), float("nan"))
    ms = sorted(mean([diffs[rng.randrange(len(diffs))] for _ in diffs]) for _ in range(n))
    return (ms[int(0.025 * n)], ms[int(0.975 * n) - 1])


def cmd_analyze(a):
    runs = [json.loads(l) for l in open(os.path.join(a.out, "runs.jsonl"), encoding="utf-8") if l.strip()]
    runs = [r for r in runs if r["exit"] == 0 and not r["result"].get("is_error")]
    by = {}
    for r in runs:
        by.setdefault((r["tier"], r["id"], r["arm"]), []).append(r)
    tier_of = {k[1]: k[0] for k in by}
    rng = random.Random(a.seed)
    report = {}
    for tier in (1, 2, 3, "all"):
        ids = sorted({k[1] for k in by if tier == "all" or k[0] == tier})
        row = {"prompts": 0}
        for metric in ("correct", "wrong", "missed"):
            diffs = []
            for pid in ids:
                c = by.get((tier_of[pid], pid, a.control))
                p = by.get((tier_of[pid], pid, a.profile))
                if not c or not p:
                    continue  # a prompt needs runs in both arms to be paired
                diffs.append(mean([float(r[metric]) for r in p]) - mean([float(r[metric]) for r in c]))
            row["prompts"] = len(diffs)
            lo, hi = bootstrap_ci(diffs, rng)
            row[metric] = {"profile_minus_control": mean(diffs), "ci95": [lo, hi]}
        toks = {}
        for arm in (a.control, a.profile):
            vals = [r["context_tokens"] for r in runs if r["arm"] == arm and r["context_tokens"]
                    and (tier == "all" or r["tier"] == tier)]
            toks[arm] = mean(vals)
        row["context_tokens_mean"] = toks
        report[str(tier)] = row
    print(json.dumps(report, indent=2))
    verdict(report, a)


def verdict(report, a):
    """Apply the protocol's fixed success criterion (section 5). Edit nothing here after the freeze."""
    t1, t2, t3 = report["1"], report["2"], report["3"]
    benefit_t2 = (t2["correct"]["profile_minus_control"] >= a.min_gain and t2["correct"]["ci95"][0] > 0)
    benefit_wrong = any(t["wrong"]["profile_minus_control"] <= -a.min_wrong_drop and t["wrong"]["ci95"][1] < 0 for t in (t2, t3))
    no_harm_t1 = t1["correct"]["ci95"][0] >= -a.max_regression  # non-inferior on explicit prompts
    ok = (benefit_t2 or benefit_wrong) and no_harm_t1
    print("\nVERDICT (protocol section 5):")
    print(f"  benefit on tier 2 correct-first-try (mean >= +{a.min_gain} and CI lower > 0): {benefit_t2}")
    print(f"  benefit on wrong activation (mean <= -{a.min_wrong_drop} and CI upper < 0, tier 2 or 3): {benefit_wrong}")
    print(f"  tier 1 not worse (CI lower >= -{a.max_regression}): {no_harm_t1}")
    print("  => " + ("BUILD the launcher as a product" if ok else "DO NOT build as a product: document the alias recipe"))


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    sub = ap.add_subparsers(dest="cmd", required=True)
    r = sub.add_parser("run")
    r.add_argument("--prompts", required=True, help="JSON lines prompt file (frozen)")
    r.add_argument("--arm", action="append", required=True, help="NAME=COMMAND, repeatable; use {prompt_file} {model} {max_turns}")
    r.add_argument("--reps", type=int, default=5)
    r.add_argument("--model", default="haiku")
    r.add_argument("--max-turns", type=int, default=1)
    r.add_argument("--timeout", type=int, default=180)
    r.add_argument("--seed", type=int, default=20261006)
    r.add_argument("--max-runs", type=int, default=2000, help="refuse to start above this many runs")
    r.add_argument("--out", required=True)
    r.add_argument("--execute", action="store_true", help="actually run claude; without it only prints the plan")
    r.set_defaults(fn=cmd_run)
    z = sub.add_parser("analyze")
    z.add_argument("--out", required=True)
    z.add_argument("--control", default="control")
    z.add_argument("--profile", default="profile")
    z.add_argument("--seed", type=int, default=20261006)
    z.add_argument("--min-gain", type=float, default=0.10)
    z.add_argument("--min-wrong-drop", type=float, default=0.05)
    z.add_argument("--max-regression", type=float, default=0.05)
    z.set_defaults(fn=cmd_analyze)
    a = ap.parse_args()
    a.fn(a)


if __name__ == "__main__":
    main()
