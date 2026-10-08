#!/usr/bin/env python3
"""Compare the real Python/Go processes on separate copies of an anonymous DB."""

import argparse
from datetime import datetime, timezone
import json
from pathlib import Path
import re
import statistics
import subprocess
import threading
import time
import uuid

from smoke_containers import docker, wait_for

ROOT = Path(__file__).resolve().parents[1]


def memory_mib(value):
    amount, unit = re.fullmatch(r"([0-9.]+)([a-zA-Z]+)", value.strip()).groups()
    return float(amount) * {"B": 1, "kB": 1000, "MB": 10**6, "GB": 10**9,
                           "KiB": 1024, "MiB": 1024**2, "GiB": 1024**3}[unit] / 1024**2


def experiment(language, args, fixture_start):
    prefix = "doggo-compare-" + uuid.uuid4().hex[:10]
    network = prefix + "-net"
    containers = []
    image = args.go_image if language == "go" else args.python_image

    def start(role, image_name, *options, command=()):
        name = prefix + "-" + role
        containers.append(name)
        docker("run", "-d", "--name", name, "--network", network,
               "--label", "dev.dota-doggo.compare=true", *options, image_name, *command)
        return name

    def sql(query):
        return docker("exec", db, "psql", "-U", "postgres", "-d", "comparison", "-At", "-c", query).stdout.strip()

    def http(path, body=None):
        command = ["exec", api, "/fixture-api", "--request", path]
        if body is not None:
            command.extend(["--data", json.dumps(body)])
        return json.loads(docker(*command).stdout)

    def send(text):
        http("/updates", {"text": text})

    def messages_containing(text):
        return [m["text"] for m in http("/observations")["messages"] or [] if text in m["text"]]

    def measure(scenario, action=lambda: None):
        before = http("/observations")
        samples = []
        failures = []
        end = time.monotonic() + args.seconds

        def sample():
            try:
                while time.monotonic() < end:
                    rows = [json.loads(line) for line in docker("stats", "--no-stream", "--format", "{{json .}}", bot, worker, db).stdout.splitlines()]
                    by_name = {row["Name"]: row for row in rows}
                    sample = {
                        "app_memory_mib": sum(memory_mib(by_name[name]["MemUsage"].split("/")[0]) for name in (bot, worker)),
                        "app_cpu_percent": sum(float(by_name[name]["CPUPerc"].rstrip("%")) for name in (bot, worker)),
                        "postgres_memory_mib": memory_mib(by_name[db]["MemUsage"].split("/")[0]),
                        "postgres_cpu_percent": float(by_name[db]["CPUPerc"].rstrip("%")),
                    }
                    if time.monotonic() <= end:
                        samples.append(sample)
            except BaseException as exc:
                failures.append(exc)

        sampler = threading.Thread(target=sample)
        started = time.monotonic()
        sampler.start()
        try:
            action()
            if time.monotonic() > end:
                raise RuntimeError("workload exceeded measurement window; increase --seconds")
            time.sleep(max(0, end - time.monotonic()))
            after = http("/observations")
            elapsed = time.monotonic() - started
        finally:
            sampler.join()
        if failures:
            raise failures[0]
        assert len(samples) >= 2, "measurement window too short"
        counts = {key: value - before["requests"].get(key, 0) for key, value in after["requests"].items()}
        counts = {key: value for key, value in counts.items() if value}
        return {"scenario": scenario, "elapsed_seconds": round(elapsed, 3),
                "samples": samples, "requests": counts,
                "messages": (after["messages"] or [])[len(before["messages"] or []):]}

    try:
        docker("network", "create", "--internal", network)
        db = start("db", "postgres:17-alpine", "--network-alias", "db", "--cpus", "1", "--memory", "512m",
                   "-e", "POSTGRES_PASSWORD=fixture-only", "-e", "POSTGRES_DB=comparison")
        wait_for(lambda: docker("exec", db, "pg_isready", "-h", "127.0.0.1", "-U", "postgres", "-d", "comparison",
                                check=False).returncode == 0, "database startup")
        # Each run restores exactly the same seven-table baseline and empty data.
        subprocess.run(["docker", "exec", "-i", db, "psql", "-U", "postgres", "-d", "comparison", "-v", "ON_ERROR_STOP=1"],
                       input=(ROOT / "migrations/schema.sql").read_text(), text=True, capture_output=True, check=True)
        api = start("api", "dota-doggo:fixture-api", "--network-alias", "api",
                    command=["--start-time", str(fixture_start)])
        wait_for(lambda: docker("exec", api, "/fixture-api", "--request", "/health", check=False).returncode == 0, "fixture startup")
        options = ["--read-only", "--user", "65532:65532", "--cap-drop", "ALL", "--security-opt", "no-new-privileges=true",
                   "--cpus", "1", "--memory", "256m", "--tmpfs", "/tmp:rw,noexec,nosuid,size=16m"]
        config = {
            "BOT_TOKEN": "123456:fixture-token", "DATABASE_URL": "postgresql://postgres:fixture-only@db:5432/comparison?sslmode=disable",
            "TELEGRAM_BASE_URL": "http://api:8080", "OPENDOTA_BASE_URL": "http://api:8080/api",
            "POLL_INTERVAL_MINUTES": "0.01", "ALLOWED_TELEGRAM_USER_IDS": "123", "DEFAULT_TIMEZONE": "UTC",
            "OPENDOTA_MAX_RETRIES": "1", "TELEGRAM_SEND_MAX_RETRIES": "1", "LOG_LEVEL": "ERROR",
        }
        if language == "python":
            config["DATABASE_URL"] = "postgresql+asyncpg://postgres:fixture-only@db:5432/comparison"
            config["POLL_INTERVAL_MINUTES"] = "1"
            options.extend(["--mount", "type=bind,source=" + str(ROOT / "scripts/python_fixture_entry.py") + ",target=/fixture-entry.py,readonly"])
        for key, value in config.items():
            options.extend(["-e", key + "=" + value])

        def process(role):
            command = [role] if language == "go" else ["/app/.venv/bin/python3", "/fixture-entry.py", role]
            return start(role, image, *options, command=command)

        bot = process("bot")
        worker = process("worker")
        wait_for(lambda: http("/observations")["requests"].get("POST /bot123456:fixture-token/getUpdates", 0) > 0, "bot polling")
        time.sleep(2)  # Same warm-up for both implementations; outside measurements.
        windows = [measure("idle")]
        send("/track 123 Fixture")
        wait_for(lambda: sql("SELECT last_seen_match_id FROM topic_players") == "999", "initial sync")
        assert not messages_containing("dotabuff.com/matches/999"), "unexpected initial notification"

        def poll():
            http("/control", {"phase": "new"})
            wait_for(lambda: sql("SELECT last_seen_match_id FROM topic_players") == "1000", "new match")
        windows.append(measure("poll", poll))
        assert len(messages_containing("dotabuff.com/matches/1000")) == 1

        report_marker = "Matches: 2 | W/L: 2/0 | WR: 100.00%"

        def reports():
            for _ in range(args.commands):
                previous = len(messages_containing(report_marker))
                send("/report month")
                wait_for(lambda: len(messages_containing(report_marker)) > previous, "report result")
        windows.append(measure("report", reports))

        def resync():
            marker = "История обновлена." if language == "go" else "Resync for last 7 day(s):"
            for _ in range(args.commands):
                previous = len(messages_containing(marker))
                send("/resync 7")
                wait_for(lambda: len(messages_containing(marker)) > previous, "resync result")
        windows.append(measure("resync", resync))
        assert sql("SELECT count(*) FROM player_matches WHERE gpm=500 AND xpm=600") == "2", "enrichment lost"
        assert sql("SELECT last_seen_match_id FROM topic_players") == "1000", "cursor regressed"
        send("/report month")
        wait_for(lambda: bool(messages_containing("GPM/XPM avg: 500.00/600.00")), "enriched report")
        final = http("/observations")
        assert all(str(m["chat_id"]) == "-100123" for m in final["messages"])
        # Compare business fields only; generated IDs, timestamps and raw JSON vary.
        snapshot = sql("SELECT p.dota_account_id,m.match_id,extract(epoch from m.start_time)::bigint,m.hero_id,m.radiant_win,m.player_slot,m.kills,m.deaths,m.assists,m.gpm,m.xpm FROM player_matches m JOIN players p ON p.id=m.player_id ORDER BY p.dota_account_id,m.match_id")
        print(language, "passed: idle, poll, report, resync", flush=True)
        return {"language": language, "windows": windows, "business_snapshot": snapshot, "observations": final}
    except BaseException:
        for name in containers:
            log = docker("logs", "--tail", "20", name, check=False)
            print(name, log.stdout, log.stderr, flush=True)
        raise
    finally:
        for name in reversed(containers):
            docker("rm", "--force", "--volumes", name, check=False)
        docker("network", "rm", network, check=False)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--go-image", default="dota-doggo:local")
    parser.add_argument("--python-image", default="dota-dog:reference")
    parser.add_argument("--rounds", type=int, default=3)
    parser.add_argument("--seconds", type=int, default=10)
    parser.add_argument("--commands", type=int, default=3)
    parser.add_argument("--output", default=".cache/runtime-comparison.json")
    args = parser.parse_args()
    if args.rounds < 1 or args.seconds < 5 or args.commands < 1:
        parser.error("rounds/commands must be positive, seconds must be >= 5")
    result = {"date_utc": datetime.now(timezone.utc).isoformat(), "settings": vars(args), "runs": [], "images": {}}
    for image in (args.go_image, args.python_image, "postgres:17-alpine", "dota-doggo:fixture-api"):
        metadata = json.loads(docker("image", "inspect", image).stdout)[0]
        result["images"][image] = {key: metadata[key] for key in ("Id", "Architecture", "Os", "Size")}
    result["docker"] = json.loads(docker("version", "--format", "{{json .Server}}").stdout)
    result["host"] = json.loads(docker("info", "--format", '{"cpus":{{.NCPU}},"memory_bytes":{{.MemTotal}},"architecture":{{json .Architecture}},"os":{{json .OperatingSystem}},"kernel":{{json .KernelVersion}}}').stdout)
    fixture_start = int(time.time()) - 3600
    result["fixture_start"] = fixture_start
    for round_number in range(args.rounds):
        order = ("go", "python") if round_number % 2 == 0 else ("python", "go")
        for language in order:
            result["runs"].append(experiment(language, args, fixture_start))
    assert len({run["business_snapshot"] for run in result["runs"]}) == 1, "business snapshots differ"
    result["summary"] = []
    for language in ("go", "python"):
        for scenario in ("idle", "poll", "report", "resync"):
            windows = [w for run in result["runs"] if run["language"] == language for w in run["windows"] if w["scenario"] == scenario]
            samples = [s for w in windows for s in w["samples"]]
            row = {"language": language, "scenario": scenario, "samples": len(samples)}
            for key in samples[0]:
                row[key + "_median"] = round(statistics.median(s[key] for s in samples), 3)
                row[key + "_max"] = round(max(s[key] for s in samples), 3)
            row["opendota_requests_median"] = statistics.median(sum(v for k, v in w["requests"].items() if " /api/" in k) for w in windows)
            row["telegram_requests_median"] = statistics.median(sum(v for k, v in w["requests"].items() if " /bot" in k) for w in windows)
            result["summary"].append(row)
    output = Path(args.output)
    output.parent.mkdir(parents=True, exist_ok=True)
    output.write_text(json.dumps(result, ensure_ascii=False, indent=2) + "\n")
    print(json.dumps(result["summary"], ensure_ascii=False, indent=2))
    print("Captured measurements and messages:", output)


if __name__ == "__main__":
    main()
