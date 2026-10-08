#!/usr/bin/env python3
"""Exercise the real images on an owned, internal Docker network, without live APIs."""

import argparse
import json
from pathlib import Path
import subprocess
import time
import uuid


def docker(*args, check=True):
    return subprocess.run(["docker", *args], text=True, capture_output=True, check=check)


def wait_for(check, description, timeout=45):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        if check():
            return
        time.sleep(0.2)
    raise RuntimeError("timeout: " + description)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--image", default="dota-doggo:local")
    parser.add_argument("--fixture-image", default="dota-doggo:fixture-api")
    parser.add_argument("--output", default=".cache/container-smoke.json")
    args = parser.parse_args()
    prefix = "doggo-smoke-" + uuid.uuid4().hex[:10]
    network = prefix + "-net"
    containers = []
    results = {"checks": [], "shutdown_seconds": {}}

    def start(name, *options, image=args.image, command=()):
        name = prefix + "-" + name
        containers.append(name)
        docker("run", "--detach", "--name", name, "--network", network,
               "--label", "dev.dota-doggo.smoke=true", *options, image, *command)
        return name

    def sql(query):
        return docker("exec", db, "psql", "-U", "postgres", "-d", "doggo_smoke",
                      "-At", "-c", query).stdout.strip()

    def healthy(name, component):
        return docker("exec", name, "/dota-doggo", "healthcheck", component,
                      check=False).returncode == 0

    def stop(name):
        started = time.monotonic()
        docker("stop", "--time", "10", name)
        elapsed = time.monotonic() - started
        code = docker("inspect", "--format", "{{.State.ExitCode}}", name).stdout.strip()
        assert code == "0", (name, code)
        assert elapsed < 10, (name, elapsed)
        results["shutdown_seconds"][name.removeprefix(prefix + "-")] = round(elapsed, 3)

    try:
        docker("network", "create", "--internal", network)
        db = start("db", "--network-alias", "db", "-e", "POSTGRES_DB=doggo_smoke",
                   "-e", "POSTGRES_PASSWORD=fixture-only", image="postgres:17-alpine")
        wait_for(lambda: docker("exec", db, "pg_isready", "-h", "127.0.0.1", "-U", "postgres",
                                "-d", "doggo_smoke", check=False).returncode == 0,
                 "PostgreSQL startup")
        api = start("api", "--network-alias", "api",
                    image=args.fixture_image)

        def http(path, body=None):
            command = ["exec", api, "/fixture-api", "--request", path]
            if body is not None:
                command.extend(["--data", json.dumps(body)])
            return json.loads(docker(*command).stdout)

        def api_up():
            try:
                return http("/health")["ok"]
            except subprocess.CalledProcessError:
                return False

        wait_for(api_up, "fixture API startup")
        options = ["--read-only", "--cap-drop", "ALL", "--security-opt", "no-new-privileges=true"]
        for key, value in {
            "BOT_TOKEN": "123456:fixture-token", "DATABASE_URL": "postgres://postgres:fixture-only@db:5432/doggo_smoke?sslmode=disable",
            "TELEGRAM_BASE_URL": "http://api:8080", "OPENDOTA_BASE_URL": "http://api:8080/api",
            "HEALTH_ADDR": "127.0.0.1:8080", "POLL_INTERVAL_MINUTES": "0.01",
            "ALLOWED_TELEGRAM_USER_IDS": "123", "DEFAULT_TIMEZONE": "UTC",
            "OPENDOTA_MAX_RETRIES": "1", "TELEGRAM_SEND_MAX_RETRIES": "1",
        }.items():
            options.extend(["-e", key + "=" + value])
        started = time.monotonic()
        bot = start("bot", *options, command=["bot"])
        worker = start("worker", *options, command=["worker"])
        wait_for(lambda: healthy(bot, "bot") and healthy(worker, "worker"), "process readiness")
        results["startup_seconds"] = round(time.monotonic() - started, 3)
        assert not healthy(bot, "worker"), "component identity was ignored"
        results["checks"].append("startup and process-specific readiness")

        http("/updates", {"text": "/track 123 Fixture"})
        wait_for(lambda: sql("SELECT count(*) FROM topic_players") == "1", "track")
        wait_for(lambda: sql("SELECT last_seen_match_id FROM topic_players") == "999", "initial cursor")
        assert not any("dotabuff.com/matches/999" in m["text"] for m in http("/observations")["messages"])
        http("/control", {"phase": "new"})
        wait_for(lambda: sql("SELECT last_seen_match_id FROM topic_players") == "1000", "new match notification")
        observed = http("/observations")
        assert sum("dotabuff.com/matches/1000" in m["text"] for m in observed["messages"]) == 1
        results["checks"].append("track, silent initial sync, one new notification")

        http("/updates", {"text": "/report month"})
        wait_for(lambda: sql("SELECT count(*) FROM report_runs WHERE trigger_source='manual'") == "1", "manual report")
        http("/updates", {"text": "/resync 7"})
        wait_for(lambda: sql("SELECT count(*) FROM player_matches WHERE gpm=500") == "2", "resync enrichment")
        wait_for(lambda: any("История обновлена." in m["text"] for m in http("/observations")["messages"]), "resync completion")
        results["checks"].append("manual report and background resync")

        http("/control", {"phase": "poll_error"})
        wait_for(lambda: not healthy(bot, "bot"), "failed polling becomes unready")
        http("/control", {"phase": "new"})
        wait_for(lambda: healthy(bot, "bot"), "polling recovers")
        results["checks"].append("polling failure and readiness recovery")

        second = start("duplicate-worker", *options, command=["worker"])
        wait_for(lambda: docker("inspect", "--format", "{{.State.Running}}", second).stdout.strip() == "false", "duplicate worker rejection")
        assert docker("inspect", "--format", "{{.State.ExitCode}}", second).stdout.strip() == "1"
        assert "worker" in docker("logs", second).stderr.lower()
        results["checks"].append("duplicate worker rejected")

        http("/control", {"phase": "block_recent"})
        before = http("/observations")["requests"].get("GET /api/players/123/recentMatches", 0)
        wait_for(lambda: http("/observations")["requests"].get("GET /api/players/123/recentMatches", 0) > before, "blocked in-flight request")
        stop(worker)
        http("/control", {"phase": "new"})
        replacement = start("replacement-worker", *options, command=["worker"])
        wait_for(lambda: healthy(replacement, "worker"), "lock released after SIGTERM")
        stop(replacement)
        stop(bot)
        results["checks"].append("SIGTERM cancels in-flight I/O, releases lock, closes polling")

        observed = http("/observations")
        assert all(str(m["chat_id"]) == "-100123" for m in observed["messages"])
        # Only the notification and report include a match URL after resync.
        assert sql("SELECT count(*) FROM player_matches") == "2"
        assert sql("SELECT last_seen_match_id FROM topic_players") == "1000"
        results["observations"] = observed
        results["image"] = json.loads(docker("image", "inspect", args.image).stdout)[0]
        # Keep only reproducible metadata, rather than the entire Docker inspection.
        results["image"] = {key: results["image"][key] for key in ("Id", "Architecture", "Os", "Size")}
        results["status"] = "passed"
        output = Path(args.output)
        output.parent.mkdir(parents=True, exist_ok=True)
        output.write_text(json.dumps(results, ensure_ascii=False, indent=2) + "\n")
        print(json.dumps({key: value for key, value in results.items() if key != "observations"}, ensure_ascii=False, indent=2))
        print("Captured messages and request counts:", output)
    except BaseException:
        for name in containers:
            log = docker("logs", "--tail", "30", name, check=False)
            print(name, log.stdout, log.stderr)
        raise
    finally:
        for name in reversed(containers):
            docker("rm", "--force", "--volumes", name, check=False)
        docker("network", "rm", network, check=False)


if __name__ == "__main__":
    main()
