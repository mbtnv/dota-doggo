#!/usr/bin/env python3
"""Run the VPS Compose on a fresh isolated DB and rehearse backup/restore."""

import json
import os
from pathlib import Path
import subprocess
import tempfile
import uuid

from smoke_containers import docker, wait_for

ROOT = Path(__file__).resolve().parents[1]


def main():
    project = "doggo-production-smoke-" + uuid.uuid4().hex[:10]
    network = project + "-net"
    api = project + "-api"
    with tempfile.TemporaryDirectory(prefix="doggo-production-") as temporary:
        directory = Path(temporary)
        env_file = directory / ".env"
        config = {
            "DOTA_DOGGO_IMAGE": "dota-doggo:local",
            "DOTA_DOGGO_ENV_FILE": str(env_file),
            "POSTGRES_PASSWORD": "fixture-only",
            "BOT_TOKEN": "123456:fixture-token",
            "TELEGRAM_BASE_URL": "http://api:8080",
            "TELEGRAM_PROXY_URL": "",
            "OPENDOTA_BASE_URL": "http://api:8080/api",
            "POLL_INTERVAL_MINUTES": "0.01",
            "ALLOWED_TELEGRAM_USER_IDS": "123",
        }
        env_file.write_text("".join(key + "=" + value + "\n" for key, value in config.items()))
        env_file.chmod(0o600)
        override = directory / "override.json"
        override.write_text(json.dumps({"networks": {"default": {"external": True, "name": network}}}))
        environment = {**os.environ, **config}

        def compose(*args, check=True):
            return subprocess.run(["docker", "compose", "-p", project, "--env-file", str(env_file),
                                   "-f", str(ROOT / "deploy/compose.yml"), "-f", str(override), *args],
                                  env=environment, text=True, capture_output=True, check=check)

        def sql(query, database="dota_doggo"):
            return compose("exec", "-T", "db", "psql", "-U", "postgres", "-d", database,
                           "-At", "-v", "ON_ERROR_STOP=1", "-c", query).stdout.strip()

        def http(path, body=None):
            args = ["exec", api, "/fixture-api", "--request", path]
            if body is not None:
                args.extend(["--data", json.dumps(body)])
            return json.loads(docker(*args).stdout)

        try:
            docker("network", "create", "--internal", network)
            docker("run", "-d", "--name", api, "--network", network, "--network-alias", "api",
                   "--label", "dev.dota-doggo.production-smoke=true", "dota-doggo:fixture-api")
            wait_for(lambda: docker("exec", api, "/fixture-api", "--request", "/health", check=False).returncode == 0,
                     "fixture API startup")
            compose("config", "--quiet")
            compose("up", "-d", "--wait", "--wait-timeout", "90")
            assert sql("SELECT count(*) FROM tracked_topics") == "0"
            assert sql("SELECT max(version_id) FROM goose_db_version WHERE is_applied") == "2"
            print("PASS VPS Compose initializes a fresh database and becomes ready", flush=True)

            http("/updates", {"text": "/track 123 Fixture"})
            wait_for(lambda: sql("SELECT last_seen_match_id FROM topic_players") == "999", "initial cursor")
            compose("restart", "bot", "worker")
            compose("up", "-d", "--wait", "--wait-timeout", "90")
            assert sql("SELECT count(*) FROM topic_players") == "1"
            assert sql("SELECT last_seen_match_id FROM topic_players") == "999"
            assert not any("dotabuff.com/matches/999" in m["text"] for m in http("/observations")["messages"])
            print("PASS restart preserves player and cursor without an initial notification", flush=True)

            compose("stop", "bot", "worker")
            sql("""INSERT INTO report_deliveries(topic_id,period_type,period_start,period_end,parts,message_ids)
                SELECT id,'day','2026-10-06T00:00:00Z','2026-10-07T00:00:00Z',ARRAY['first','second'],ARRAY[42::bigint]
                FROM tracked_topics LIMIT 1""")
            sql("""INSERT INTO match_deliveries(topic_id,match_id,player_ids,parts,message_ids)
                SELECT topic_id,1000,ARRAY[player_id::bigint],ARRAY['first','second'],ARRAY[43::bigint]
                FROM topic_players LIMIT 1""")
            backup = subprocess.run(["docker", "compose", "-p", project, "--env-file", str(env_file),
                                     "-f", str(ROOT / "deploy/compose.yml"), "-f", str(override),
                                     "exec", "-T", "db", "pg_dump", "-U", "postgres", "-d", "dota_doggo", "-Fc"],
                                    env=environment, capture_output=True, check=True).stdout
            assert backup.startswith(b"PGDMP")
            sql("CREATE DATABASE rehearsal")
            db = compose("ps", "-q", "db").stdout.strip()
            subprocess.run(["docker", "exec", "-i", db, "pg_restore", "-U", "postgres", "-d", "rehearsal",
                            "--exit-on-error"], input=backup, capture_output=True, check=True)
            compose("run", "--rm", "--no-deps", "-e",
                    "DATABASE_URL=postgres://postgres:fixture-only@db:5432/rehearsal?sslmode=disable", "bot", "migrate")
            assert sql("SELECT last_seen_match_id FROM topic_players", "rehearsal") == "999"
            assert sql("SELECT count(*) FROM player_matches", "rehearsal") == "1"
            assert sql("SELECT message_ids[1] FROM report_deliveries", "rehearsal") == "42"
            assert sql("SELECT message_ids[1] FROM match_deliveries", "rehearsal") == "43"
            print("PASS backup restores history, cursors and partial deliveries; Go migrations pass", flush=True)
        except BaseException as error:
            if isinstance(error, subprocess.CalledProcessError):
                print(error.stdout, error.stderr)
            logs = compose("logs", "--tail", "30", check=False)
            print(logs.stdout, logs.stderr)
            raise
        finally:
            compose("down", "--volumes", check=False)
            docker("rm", "--force", "--volumes", api, check=False)
            docker("network", "rm", network, check=False)


if __name__ == "__main__":
    main()
