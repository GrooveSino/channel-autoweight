#!/usr/bin/env python3
"""Local-only improvement ledger. Never invokes the live runner, network or git."""
import argparse
import fcntl
import json
import os
from pathlib import Path
import re
import tempfile
from contextlib import contextmanager
from datetime import datetime, timezone, timedelta

ZONE = timezone(timedelta(hours=8))
CUTOFF = "2026-11-21T01:43:00+08:00"
DEFAULT_ROOT = Path("/workspace/ops/channel-weight-only/runs")
DEFAULT_STATE = Path("/workspace/ops/channel-weight-only/learning/state.json")
RUNTIME = {"version": "v5.01", "binary_sha256": "0db48260f84ef1b93b365e023e96c4556355b595fcdc502ddfdbb5ee4c109b79"}


def now():
    return datetime.now(ZONE).isoformat()


def timestamp(value):
    dt = datetime.fromisoformat(value)
    if dt.tzinfo is None:
        raise ValueError("timestamp must include a timezone")
    return dt


def read_json(path):
    with path.open(encoding="utf-8") as f:
        return json.load(f)


def fresh():
    return {"schema_version": 1, "effective_after": CUTOFF, "created_at": now(),
            "running_version_at_initialization": RUNTIME.copy(), "historical_seeds": {},
            "rounds": {}, "reviews": {}, "maintenance": {}}


@contextmanager
def locked_state(path):
    path = Path(path)
    path.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
    fd = os.open(path.with_name("state.lock"), os.O_CREAT | os.O_RDWR, 0o600)
    with os.fdopen(fd, "a") as lock:
        fcntl.flock(lock, fcntl.LOCK_EX)
        state = read_json(path) if path.exists() else fresh()
        if state.get("schema_version") != 1 or state.get("effective_after") != CUTOFF:
            raise ValueError("unsupported existing state; preserve it and migrate explicitly")
        for key in ("rounds", "reviews", "historical_seeds", "maintenance"):
            if not isinstance(state.get(key), dict):
                raise ValueError("invalid state section: " + key)
        yield state


def save(path, state):
    """Caller holds state.lock. Durable replacement; never truncate the old state."""
    path = Path(path)
    fd, name = tempfile.mkstemp(prefix=".learning-", dir=path.parent)
    try:
        with os.fdopen(fd, "w", encoding="utf-8") as f:
            os.fchmod(f.fileno(), 0o600)
            json.dump(state, f, ensure_ascii=False, indent=2)
            f.write("\n")
            f.flush()
            os.fsync(f.fileno())
        os.replace(name, path)
        dfd = os.open(path.parent, os.O_RDONLY | os.O_DIRECTORY)
        try:
            os.fsync(dfd)
        finally:
            os.close(dfd)
    finally:
        if os.path.exists(name):
            os.unlink(name)


def run_info(run_dir, root, outcome="auto"):
    path = Path(run_dir).resolve(strict=True)
    if not path.is_dir() or path.parent != Path(root).resolve(strict=True):
        raise ValueError("run_dir must resolve to one direct child of runs_root")
    status_path, result_path = path / "status.json", path / "result.json"
    status = read_json(status_path) if status_path.exists() else {}
    result = read_json(result_path) if result_path.exists() else {}
    info = {**status, **result}
    if info.get("mode") != "run":
        raise ValueError("not a real run: check-only and manual inspections do not count")
    started = info.get("started")
    timestamp(started)  # reject missing/naive timestamps
    terminal = info.get("state", "")
    if terminal == "verified":
        if outcome != "auto":
            raise ValueError("verified result cannot be relabeled failed/uncertain")
        label = "verified_write" if info.get("applied") is True else "verified_noop"
    elif terminal in ("failed", "error", "aborted", "uncertain"):
        label = "uncertain" if terminal == "uncertain" else "failed"
        if outcome != "auto" and outcome != label:
            raise ValueError("outcome conflicts with terminal artifact")
    elif outcome in ("failed", "uncertain") and not result:
        label = outcome  # explicit operator classification after a failed runner
    else:
        raise ValueError("run is not terminal; explicit failed/uncertain needed if result is missing")
    return str(path), {"run_id": path.name, "started": started,
                       "outcome": label, "applied": (label == "verified_write") if label.startswith("verified_") else None,
                       "evidence": [str(p) for p in (status_path, result_path) if p.exists()]}


def summary(state):
    count = len(state["rounds"])
    due = [i for i in range(1, count // 4 + 1) if str(i) not in state["reviews"]]
    return {"counted_rounds": count, "historical_seed_count": len(state["historical_seeds"]),
            "next_ordinal": count + 1, "next_review_at": (count // 4 + 1) * 4,
            "review_due": due, "running_version_at_initialization": state["running_version_at_initialization"]}


def initialize(path, root):
    with locked_state(path) as state:
        for run in sorted(Path(root).iterdir()):
            if not run.is_dir():
                continue
            try:
                key, info = run_info(run, root)
            except (ValueError, TypeError, FileNotFoundError):
                continue
            if timestamp(info["started"]) < timestamp(CUTOFF):
                state["historical_seeds"].setdefault(key, info)
        # Initialization never silently enrolls newer runs.
        save(path, state)
        return summary(state)


def validate_meta(meta):
    # Explicit human summaries only: never load raw logs, config, credentials or git diff.
    for case in meta.get("candidate_improvements", []):
        if not re.fullmatch(r"CASE-AW-[0-9]{3}", case):
            raise ValueError("invalid stable case id")
    commit = meta.get("commit")
    if commit and not re.fullmatch(r"[a-fA-F0-9]{7,40}", commit):
        raise ValueError("commit must be a git hash, not a URL or arbitrary text")
    if any(len(s) > 1500 for s in meta.get("tests", []) + [meta.get("note", "")]):
        raise ValueError("summary too long; do not paste raw logs")
    return meta


def record(path, root, run_dir, meta, outcome="auto"):
    validate_meta(meta)
    key, info = run_info(run_dir, root, outcome)
    if timestamp(info["started"]) < timestamp(CUTOFF):
        raise ValueError("pre-instruction run is a historical seed, not a counted round")
    with locked_state(path) as state:
        if key in state["rounds"]:
            return {**summary(state), "duplicate": True, "round": state["rounds"][key]}
        info.update(ordinal=len(state["rounds"]) + 1, registered_at=now(), improvement=meta)
        state["rounds"][key] = info
        save(path, state)
        return {**summary(state), "duplicate": False, "round": info}


def annotate(path, run_dir, meta):
    validate_meta(meta)
    key = str(Path(run_dir).resolve(strict=True))
    with locked_state(path) as state:
        if key not in state["rounds"]:
            raise ValueError("record the real round before annotating it")
        entry = state["rounds"][key]
        entry.setdefault("improvement_history", []).append(entry["improvement"])
        entry["improvement"] = meta
        save(path, state)
        return summary(state)


def review(path, cycle, conclusion, case_ids, commit=None):
    validate_meta({"candidate_improvements": case_ids, "commit": commit, "note": conclusion})
    if not conclusion.strip():
        raise ValueError("review requires a substantive conclusion, even with no new cases")
    with locked_state(path) as state:
        if cycle < 1 or len(state["rounds"]) < cycle * 4:
            raise ValueError("cycle needs four unique real rounds; initialization never counts")
        if str(cycle) in state["reviews"]:
            return {**summary(state), "duplicate_review": True}
        ordinals = list(range(cycle * 4 - 3, cycle * 4 + 1))
        state["reviews"][str(cycle)] = {"completed_at": now(), "ordinals": ordinals,
                "conclusion": conclusion, "case_ids": case_ids, "commit": commit}
        save(path, state)
        return summary(state)


def maintenance(path, entry_id, meta):
    validate_meta(meta)
    if not re.fullmatch(r"[A-Za-z0-9_.-]{1,80}", entry_id):
        raise ValueError("invalid maintenance id")
    with locked_state(path) as state:
        state["maintenance"].setdefault(entry_id, {"at": now(), **meta})
        save(path, state)
        return summary(state)


def metadata(args):
    return {"candidate_improvements": args.candidate, "change_kind": args.change_kind,
            "code_modified": args.change_kind in ("tests", "tooling", "algorithm"),
            "algorithm_behavior_changed": args.change_kind == "algorithm",
            "tests": args.tests, "commit": args.commit,
            "candidate_version": args.candidate_version, "running_version": args.runtime_version,
            "running_binary_sha256": RUNTIME["binary_sha256"], "note": args.note}


def add_meta(parser):
    parser.add_argument("--candidate", action="append", default=[])
    parser.add_argument("--change-kind", choices=["none", "docs", "tests", "tooling", "algorithm"], default="none")
    parser.add_argument("--tests", action="append", default=[])
    parser.add_argument("--commit")
    parser.add_argument("--candidate-version", default="no-new-version")
    parser.add_argument("--runtime-version", choices=["v5.01"], default="v5.01")
    parser.add_argument("--note", default="not yet reviewed")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--state-file", type=Path, default=DEFAULT_STATE)
    parser.add_argument("--runs-root", type=Path, default=DEFAULT_ROOT)
    sub = parser.add_subparsers(dest="action", required=True)
    sub.add_parser("init"); sub.add_parser("status")
    for action in ("record", "annotate"):
        p = sub.add_parser(action); p.add_argument("--run-dir", required=True, type=Path); add_meta(p)
        if action == "record": p.add_argument("--outcome", choices=["auto", "failed", "uncertain"], default="auto")
    p = sub.add_parser("review"); p.add_argument("--cycle", required=True, type=int)
    p.add_argument("--conclusion", required=True); p.add_argument("--case-id", action="append", default=[]); p.add_argument("--commit")
    p = sub.add_parser("maintenance"); p.add_argument("--id", required=True); add_meta(p)
    args = parser.parse_args()
    try:
        if args.action == "init": result = initialize(args.state_file, args.runs_root)
        elif args.action == "status":
            with locked_state(args.state_file) as state: result = summary(state)
        elif args.action == "record": result = record(args.state_file, args.runs_root, args.run_dir, metadata(args), args.outcome)
        elif args.action == "annotate": result = annotate(args.state_file, args.run_dir, metadata(args))
        elif args.action == "review": result = review(args.state_file, args.cycle, args.conclusion, args.case_id, args.commit)
        else: result = maintenance(args.state_file, args.id, metadata(args))
    except (ValueError, TypeError, OSError, json.JSONDecodeError) as exc:
        parser.exit(2, "learning-state refused: " + str(exc) + "\n")
    print(json.dumps(result, ensure_ascii=False, indent=2))


if __name__ == "__main__":
    main()
