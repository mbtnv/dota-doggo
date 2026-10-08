"""Export anonymous reference examples using the original project's Python venv.

Run: PYTHONDONTWRITEBYTECODE=1 ../dota-dog/.venv/bin/python scripts/export_python_contract.py
Never reads .env, Telegram credentials, or a database.
"""
from __future__ import annotations

import ast
import csv
import importlib.util
import io
import json
import subprocess
import sys
from dataclasses import asdict
from datetime import UTC, datetime, timedelta
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
SOURCE = (ROOT / "../dota-dog").resolve()
EXPECTED = "1b77e26702e778a13b5b1e5b6e7a968feb5ff68c"
commit = subprocess.check_output(["git", "-C", str(SOURCE), "rev-parse", "HEAD"], text=True).strip()
if commit != EXPECTED:
    raise SystemExit(f"Source revision changed: {commit}; review reference export before updating")
sys.path.insert(0, str(SOURCE / "src"))

from alembic.migration import MigrationContext
from alembic.operations import Operations
from sqlalchemy.schema import CreateTable
from dota_dog.domain.enums import PeriodType
from dota_dog.domain.models import ConstantSnapshot, MatchSnapshot, TrackedPlayerRef
from dota_dog.services.formatter import MessageFormatter
from dota_dog.services.match_statistics import GovnoedstvoCalculator
from dota_dog.services.reporting import ReportingService


def load_module(path: Path):
    spec = importlib.util.spec_from_file_location(path.stem, path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def dump(name: str, data: object):
    (ROOT / "testdata" / name).write_text(json.dumps(data, ensure_ascii=False, indent=2,
        default=lambda obj: obj.isoformat() if isinstance(obj, datetime) else str(obj)) + "\n")


(ROOT / "testdata").mkdir(exist_ok=True)
(ROOT / "migrations").mkdir(exist_ok=True)
buf = io.StringIO()
ctx = MigrationContext.configure(dialect_name="postgresql", opts={"as_sql": True, "output_buffer": buf})
with Operations.context(ctx):
    for path in sorted((SOURCE / "src/dota_dog/infra/db/migrations/versions").glob("*.py")):
        load_module(path).upgrade()
schema = buf.getvalue()
schema += str(CreateTable(ctx._version).compile(dialect=ctx.dialect)) + ";\n"
schema += "INSERT INTO alembic_version (version_num) VALUES ('20260505_000004');\n"
schema = "\n".join(line.rstrip() for line in schema.splitlines()) + "\n"
(ROOT / "migrations/schema.sql").write_text(("-- Generated from the four original Alembic upgrades; review changes.\n" + schema).rstrip() + "\n")
legacy = schema + """
INSERT INTO tracked_topics(id,telegram_chat_id,telegram_thread_id,title,timezone,is_paused,created_at)
VALUES(41,-1001234567890,NULL,'Example','Europe/Moscow',true,'2026-03-10T12:00:00Z');
INSERT INTO players(id,dota_account_id,display_name,profile_url,created_at)
VALUES(41,123456789,'Example player',NULL,'2026-03-10T12:00:00Z');
INSERT INTO topic_players(id,topic_id,player_id,alias,last_seen_match_id,added_by_telegram_user_id,created_at)
VALUES(41,41,41,'mid',999,NULL,'2026-03-10T12:00:00Z');
INSERT INTO player_matches(player_id,match_id,start_time,end_time,hero_id,radiant_win,player_slot,kills,deaths,assists,gpm,xpm,
hero_damage,tower_damage,hero_healing,last_hits,game_mode,lobby_type,party_size,raw_payload,created_at)
VALUES(41,999,'2026-03-10T14:25:00Z','2026-03-10T15:00:00Z',74,true,0,5,5,10,700,800,
23000,5000,0,320,22,7,2,'{"example":true}','2026-03-10T15:01:00Z');
INSERT INTO report_runs(topic_id,period_type,period_start,period_end,trigger_source,telegram_message_id,created_at)
VALUES(41,'day','2026-03-09T21:00:00Z','2026-03-10T21:00:00Z','auto',123,'2026-03-10T21:05:00Z');
INSERT INTO topic_runtime_state(topic_id,last_poll_started_at,last_poll_finished_at,last_poll_succeeded_at,last_poll_error,created_at,updated_at)
VALUES(41,'2026-03-10T15:00:00Z','2026-03-10T15:01:00Z','2026-03-10T15:01:00Z',NULL,'2026-03-10T12:00:00Z','2026-03-10T15:01:00Z');
INSERT INTO constant_entries(resource,code,name,raw_payload,created_at,updated_at)
VALUES('heroes',74,'Invoker','{"localized_name":"Invoker"}','2026-03-10T12:00:00Z','2026-03-10T12:00:00Z');
SELECT setval(pg_get_serial_sequence('players','id'),41);
SELECT setval(pg_get_serial_sequence('tracked_topics','id'),41);
SELECT setval(pg_get_serial_sequence('topic_players','id'),41);
"""
(ROOT / "testdata/legacy-schema.sql").write_text(("-- Anonymous legacy fixture exported from Python migrations.\n" + legacy).rstrip() + "\n")

targets = {
    "reporting": "domain: periods, summaries, filtering",
    "match_statistics": "domain: statistic registry and rounding",
    "tracking": "domain: snapshots and cursors",
    "formatter": "format: notifications and grouping",
    "handler_utils": "bot: argument parsing",
    "handlers": "bot: command scenarios",
    "permissions": "bot: permissions",
    "topic_repository": "postgres: topic identity and settings",
    "runtime_state": "postgres: runtime status",
    "logging": "logging: redaction",
    "backfill_service": "service: history and partial failures",
    "constants_service": "service: constants cache",
    "poll_matches_job": "jobs: topic cursors and notifications",
    "send_reports_job": "jobs: previous periods and retries",
    "opendota_client": "opendota: rate limits and HTTP retries",
    "opendota_schemas": "opendota: sparse DTO",
    "telegram_bot": "telegram: proxy transport",
    "telegram_messages": "telegram: HTML splitting",
    "telegram_retry": "telegram: recovery",
}
implemented = {
    "test_poll_job_initial_sync_persists_without_notification": "jobs.TestTrackingPollingReportsAndRestart",
    "test_poll_job_notifies_only_once_for_new_match": "jobs.TestIndependentTopicCursorsAndSharedGroup",
    "test_poll_job_rolls_back_last_seen_when_notification_fails": "jobs.TestFailedSendKeepsEarlierGamesAndRetriesOnlyUnacknowledged",
    "test_poll_job_uses_cached_constants_when_constants_sync_fails": "jobs.TestPollingUsesCachedConstantsWhenRefreshFails",
    "test_poll_job_groups_shared_match_into_single_notification": "jobs.TestIndependentTopicCursorsAndSharedGroup",
    "test_send_reports_job_is_idempotent_for_same_period": "jobs.TestConcurrentAutoReportAttemptsSendOnce",
    "test_send_reports_job_skips_zero_match_players_in_day_report": "jobs.TestAutoReportUsesLocalPreviousPeriodAndManualDoesNotSuppressIt",
    "test_build_match_snapshots_filters_by_last_seen_match_id": "domain.TestNewMatchesFiltersCursorDeduplicatesAndOrders",
    "test_backfill_service_persists_matches_and_updates_last_seen": "service.TestHistoryRefreshesStoredSparseMatchAndKeepsTopicCursorsIndependent",
    "test_backfill_service_refreshes_existing_sparse_matches": "service.TestHistoryRefreshesStoredSparseMatchAndKeepsTopicCursorsIndependent",
    "test_constants_service_syncs_snapshot_to_db": "service.TestCacheSkipsFreshAndPreservesCacheOnFailure;postgres.TestRuntimeReportsAndConstants",
    "test_format_match_notification_contains_required_fields": "format.TestFormatterReference",
    "test_format_recent_matches_groups_shared_players_into_one_block": "format.TestGroupingPreservesPlayerOrderAndDeduplicates",
    "test_format_match_notification_uses_all_pick_for_game_mode_22_without_constants": "format.TestMode22IsAllPickWithAndWithoutConstants",
    "test_format_match_notification_normalizes_all_draft_from_constants": "format.TestFormatterReference",
    "test_format_match_notification_uses_requested_timezone": "format.TestFormatterReference",
    "test_parse_account_id_from_plain_number": "bot.TestCommandMentionsAndArguments",
    "test_parse_account_id_from_profile_url": "bot.TestCommandMentionsAndArguments",
    "test_help_handler_lists_available_commands": "bot.TestCommandsPersistSettingsReportsAndRouteTopics",
    "test_limits_handler_returns_current_rate_limits": "bot.TestCommandsPersistSettingsReportsAndRouteTopics",
    "test_track_handler_adds_player_from_profile_url": "bot.TestCommandsPersistSettingsReportsAndRouteTopics",
    "test_status_handler_returns_extended_topic_summary": "bot.TestCommandsPersistSettingsReportsAndRouteTopics",
    "test_report_handler_returns_html_report_for_filtered_player": "bot.TestCommandsPersistSettingsReportsAndRouteTopics",
    "test_report_handler_skips_zero_match_players_in_day_report": "bot.TestCommandsPersistSettingsReportsAndRouteTopics",
    "test_last_handler_groups_shared_match_into_single_block": "bot.TestCommandsPersistSettingsReportsAndRouteTopics",
    "test_last_handler_keeps_shared_players_when_filtered": "bot.TestLastCommandSplitsLongHTMLWithoutDroppingSharedPlayers",
    "test_last_handler_splits_long_html_response": "bot.TestLastCommandSplitsLongHTMLWithoutDroppingSharedPlayers",
    "test_resync_handler_reports_progress_and_result": "service.TestResyncReportsSuccessfulCounts;bot.TestCommandsPersistSettingsReportsAndRouteTopics",
    "test_resync_handler_reports_errors_in_chat": "service.TestResyncProgressAndPartialFailureSummary",
    "test_build_rate_limit_snapshot_reads_headers": "opendota.TestRateLimits",
    "test_rate_limit_delay_uses_remaining_minute_header": "opendota.TestRateLimits",
    "test_rate_limit_delay_waits_until_next_minute_when_bucket_empty": "opendota.TestRateLimits",
    "test_rate_limit_delay_uses_remaining_day_when_quota_is_low": "opendota.TestRateLimits",
    "test_rate_limit_delay_is_zero_without_server_date": "opendota.TestRateLimits",
    "test_player_match_allows_missing_optional_metrics": "opendota.TestSparseMatchAndValidation",
    "test_permission_service_allows_allowlisted_user": "bot.TestPermissionsAndPrivateRouting",
    "test_permission_service_checks_chat_admins": "bot.TestPermissionsAndPrivateRouting",
    "test_settings_normalize_blank_proxy_url": "config.TestBlankProxyIsDisabled;telegram.TestInvalidProxyAndExplicitTransport",
    "test_create_bot_uses_proxy_url": "telegram.TestHTTPAndHTTPSProxy;telegram.TestSOCKSProxyRemoteDNS",
    "test_create_bot_without_proxy": "telegram.TestInvalidProxyAndExplicitTransport",
    "test_split_html_message_keeps_html_balanced": "format.TestSplitHTMLPreservesUnicodeEntitiesAndTags",
    "test_split_html_sections_keeps_sections_whole": "format.TestSplitSectionsKeepsGamesWhole",
    "test_retry_telegram_network_errors_uses_exponential_backoff": "telegram.TestNetworkRetriesBackoffAndRedactsToken",
    "test_retry_telegram_network_errors_does_not_hide_other_failures": "telegram.TestTelegramPermanentFailureReturnsPartialIDs",
    "test_redact_sensitive_text_masks_telegram_token_and_explicit_secret": "logging.TestRedactsMessagesErrorsGroupsAndBoundAttributes",
    "test_redacting_formatter_masks_token_in_traceback": "logging.TestRedactsMessagesErrorsGroupsAndBoundAttributes",
    "test_topic_repository_updates_timezone_and_pause": "postgres.TestTopicIdentityPlayersSettingsAndCursors",
    "test_main_chat_topic_is_unique_when_thread_id_is_null": "postgres.TestTopicIdentityPlayersSettingsAndCursors",
    "test_topic_runtime_repository_tracks_success_state": "postgres.TestRuntimeReportsAndConstants",
    "test_build_summary_calculates_winrate_and_streaks": "domain.TestSummaryAgainstReference",
    "test_build_topic_summaries_filters_by_alias": "domain.TestSelectPlayersPreservesAmbiguityForCaller",
    "test_previous_day_bounds_keep_local_midnight_across_dst_start": "domain.TestCalendarPeriodsAgainstReference",
    "test_previous_week_bounds_keep_local_midnight_across_dst_start": "domain.TestCalendarPeriodsAgainstReference",
    "test_govnoedstvo_calculator_uses_kda_balance": "domain.TestStatisticsAgainstReference",
    "test_match_statistics_service_returns_default_custom_statistic": "domain.TestStatisticsAgainstReference",
    "test_match_statistics_service_supports_custom_calculator_registration": "domain.TestCustomCalculator",
}
with (ROOT / "docs/python-test-map.csv").open("w", newline="") as stream:
    writer = csv.writer(stream, lineterminator="\n")
    writer.writerow(["source_file", "source_test", "go_scenario", "status", "go_test"])
    for path in sorted((SOURCE / "tests").glob("test_*.py")):
        for node in ast.parse(path.read_text()).body:
            if isinstance(node, (ast.FunctionDef, ast.AsyncFunctionDef)) and node.name.startswith("test_"):
                writer.writerow([path.name, node.name, targets[path.stem.removeprefix("test_")],
                    "implemented" if node.name in implemented else "planned", implemented.get(node.name, "")])

formatter = MessageFormatter()
reporting = ReportingService()
player = TrackedPlayerRef(1, 123456789, "Example <player>", None, "mid & carry", None)
second = TrackedPlayerRef(2, 987654321, "Second", None, None, 998)
ended = datetime(2026, 3, 10, 15, tzinfo=UTC)
matches = [
    MatchSnapshot(1, 999, ended - timedelta(minutes=35), ended, 74, True, 0,
                  5, 5, 10, 700, 800, 23000, 5000, 0, 320, 22, 7, 2, {}),
    MatchSnapshot(2, 999, ended - timedelta(minutes=35), ended, 5, True, 129,
                  0, 6, 1, 300, 400, 9000, 0, 5000, 30, 22, 7, None, {}),
]
constants = ConstantSnapshot({74: "Invoker", 5: "Crystal Maiden"}, {22: "All Draft"}, {7: "Ranked"})
summary_matches = [matches[0], MatchSnapshot(**{**asdict(matches[0]), "match_id": 1000, "radiant_win": False, "end_time": ended + timedelta(hours=1)})]
summary = reporting.build_summary(player_id=1, label=player.alias, period_type=PeriodType.DAY,
    period_start=ended.replace(hour=0), period_end=ended.replace(hour=0) + timedelta(days=1), matches=summary_matches)
dump("reference.json", {
    "source_commit": commit,
    "players": [asdict(player), asdict(second)],
    "matches": [asdict(m) for m in matches],
    "summary_matches": [asdict(m) for m in summary_matches],
    "summary": asdict(summary),
    "notification": formatter.format_match_notification(player, matches[0], constants, "Europe/Moscow"),
    "group": formatter.format_match_group_notification(list(zip([player, second], matches)), constants, "Europe/Moscow"),
    "report": formatter.format_report(summary, constants),
    "statistics": [{"kills": k, "deaths": d, "assists": a, "percent": GovnoedstvoCalculator.calculate_percent(k, d, a)}
        for k, d, a in [(0,6,1), (0,10,11), (1,14,1), (12,2,26), (9,2,12), (15,8,27), (5,5,10), (7,1,3)]],
})
periods = []
for tz, now in [("Europe/Moscow", datetime(2026,1,1,1,tzinfo=UTC)),
                ("America/New_York", datetime(2026,3,9,12,tzinfo=UTC)),
                ("America/New_York", datetime(2026,11,2,12,tzinfo=UTC))]:
    for period in PeriodType:
        for previous in (False, True):
            method = reporting.previous_period_bounds if previous else reporting.calculate_period_bounds
            start, end = method(period, now, tz)
            periods.append({"timezone": tz, "now": now, "period": period.value,
                            "previous": previous, "start": start, "end": end})
dump("periods.json", periods)
dump("opendota-recent.json", [{"match_id": 999, "player_slot": 0, "radiant_win": True,
    "duration": 2100, "game_mode": 22, "lobby_type": 7, "hero_id": 74,
    "start_time": 1773152700, "kills": 5, "deaths": 5, "assists": 10}])
dump("telegram-updates.json", [{"update_id": 1, "message": {"message_id": 10,
    "message_thread_id": 42, "date": 1773154800, "chat": {"id": -1001234567890,
    "type": "supergroup", "title": "Example"}, "from": {"id": 123456789, "is_bot": False,
    "first_name": "Example"}, "text": "/track 123456789 mid"}}])
print("Exported schema, test map and anonymous reference fixtures")
