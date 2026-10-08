//go:build integration

package migrations_test

import (
	"os"
	"strings"
	"testing"

	"dota-doggo/internal/testdb"
	"dota-doggo/migrations"
)

func TestBootstrapAndRepeatedMigration(t *testing.T) {
	ctx, db, _ := testdb.Open(t, false)
	if err := migrations.Up(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := migrations.Up(ctx, db); err != nil {
		t.Fatal("repeat:", err)
	}
	v, err := migrations.Version(ctx, db)
	if err != nil || v != migrations.CurrentVersion {
		t.Fatalf("version %d: %v", v, err)
	}
	var rev string
	if err := db.QueryRowContext(ctx, "SELECT version_num FROM alembic_version").Scan(&rev); err != nil || rev != migrations.LegacyRevision {
		t.Fatalf("legacy marker: %q %v", rev, err)
	}
}
func TestAdoptsLegacyWithoutChangingDataOrSequences(t *testing.T) {
	ctx, db, _ := testdb.Open(t, false)
	fixture, err := os.ReadFile("../testdata/legacy-schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, string(fixture)); err != nil {
		t.Fatal(err)
	}
	var before string
	const dump = `SELECT json_build_object(
	 'players',(SELECT json_agg(p ORDER BY id) FROM players p),
	 'topics',(SELECT json_agg(p ORDER BY id) FROM tracked_topics p),
	 'relations',(SELECT json_agg(p ORDER BY id) FROM topic_players p),
	 'matches',(SELECT json_agg(p ORDER BY id) FROM player_matches p),
	 'reports',(SELECT json_agg(p ORDER BY id) FROM report_runs p),
	 'runtime',(SELECT json_agg(p ORDER BY id) FROM topic_runtime_state p),
	 'constants',(SELECT json_agg(p ORDER BY id) FROM constant_entries p))::text`
	if err := db.QueryRowContext(ctx, dump).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if err := migrations.Up(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := migrations.Up(ctx, db); err != nil {
		t.Fatal(err)
	}
	var after string
	if err := db.QueryRowContext(ctx, dump).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatalf("legacy data changed: %s -> %s", before, after)
	}
	var id int64
	if err := db.QueryRowContext(ctx, "INSERT INTO players(dota_account_id,display_name,created_at) VALUES(987654321,'new',now()) RETURNING id").Scan(&id); err != nil || id != 42 {
		t.Fatalf("sequence changed: %d %v", id, err)
	}
}
func TestRejectsIncompatibleSchemas(t *testing.T) {
	for _, tc := range []struct{ name, mutation string }{
		{"old_revision", "UPDATE alembic_version SET version_num='20260310_000003'"},
		{"partial", "DROP TABLE constant_entries"},
		{"missing_column", "ALTER TABLE players DROP COLUMN profile_url"},
		{"wrong_type", "ALTER TABLE players ALTER COLUMN display_name TYPE text"},
		{"missing_unique", "DROP INDEX uq_tracked_topics_chat_null_thread"},
		{"missing_fk", "ALTER TABLE topic_players DROP CONSTRAINT topic_players_player_id_fkey"},
		{"extra_unique_index", "CREATE UNIQUE INDEX restrictive ON players(display_name)"},
		{"extra_constraint", "ALTER TABLE players ADD CONSTRAINT restrictive CHECK(dota_account_id<1000)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, db, _ := testdb.Open(t, false)
			fixture, err := os.ReadFile("../testdata/legacy-schema.sql")
			if err != nil {
				t.Fatal(err)
			}
			if tc.name == "extra_constraint" {
				tc.mutation = "ALTER TABLE players ADD CONSTRAINT restrictive CHECK(dota_account_id<1000000000)"
			}
			if _, err := db.ExecContext(ctx, string(fixture)); err != nil {
				t.Fatal(err)
			}
			if _, err := db.ExecContext(ctx, tc.mutation); err != nil {
				t.Fatal(err)
			}
			if err := migrations.Up(ctx, db); err == nil {
				t.Fatal("accepted incompatible schema")
			}
			version, err := migrations.Version(ctx, db)
			if err != nil || version != 0 {
				t.Fatalf("baseline recorded after refusal: %d %v", version, err)
			}
			var count int
			if err := db.QueryRowContext(ctx, "SELECT count(*) FROM players WHERE dota_account_id=123456789").Scan(&count); err != nil || count != 1 {
				t.Fatalf("existing data changed: %d %v", count, err)
			}
		})
	}
}
func TestVersionedSchemaStillValidated(t *testing.T) {
	ctx, db, _ := testdb.Open(t, true)
	if _, err := db.ExecContext(ctx, "ALTER TABLE players DROP COLUMN profile_url"); err != nil {
		t.Fatal(err)
	}
	if err := migrations.Up(ctx, db); err == nil || !strings.Contains(err.Error(), "incompatible") {
		t.Fatalf("expected rejection: %v", err)
	}
}

func TestRejectsNewerGoVersion(t *testing.T) {
	ctx, db, _ := testdb.Open(t, true)
	if _, err := db.ExecContext(ctx, "INSERT INTO goose_db_version(version_id,is_applied) VALUES(3,true)"); err != nil {
		t.Fatal(err)
	}
	if err := migrations.Up(ctx, db); err == nil || !strings.Contains(err.Error(), "newer") {
		t.Fatalf("expected refusal: %v", err)
	}
}

func TestRejectsVersionMarkerWithoutSchema(t *testing.T) {
	ctx, db, _ := testdb.Open(t, true)
	if _, err := db.ExecContext(ctx, "DROP TABLE topic_players,player_matches,report_runs,topic_runtime_state,constant_entries,players,tracked_topics,alembic_version CASCADE"); err != nil {
		t.Fatal(err)
	}
	if err := migrations.Up(ctx, db); err == nil {
		t.Fatal("accepted version marker without schema")
	}
}

func TestAdoptionAllowsRenamingIndexesAndAdditionalReadIndex(t *testing.T) {
	ctx, db, _ := testdb.Open(t, false)
	fixture, err := os.ReadFile("../testdata/legacy-schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, string(fixture)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "ALTER INDEX uq_tracked_topics_chat_null_thread RENAME TO renamed_main_topic; CREATE INDEX extra_read_index ON player_matches(hero_id)"); err != nil {
		t.Fatal(err)
	}
	if err := migrations.Up(ctx, db); err != nil {
		t.Fatal(err)
	}
}

func TestUpgradeBaselineOnePreservesDataAndDeliveryProgress(t *testing.T) {
	ctx, db, _ := testdb.Open(t, false)
	fixture, err := os.ReadFile("../testdata/legacy-schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, string(fixture)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `CREATE TABLE goose_db_version (
	 id INTEGER GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY, version_id BIGINT NOT NULL,
	 is_applied BOOLEAN NOT NULL, tstamp TIMESTAMP DEFAULT now());
	 INSERT INTO goose_db_version(version_id,is_applied) VALUES(0,true),(1,true)`); err != nil {
		t.Fatal(err)
	}
	if err := migrations.Up(ctx, db); err != nil {
		t.Fatal(err)
	}
	if version, err := migrations.Version(ctx, db); err != nil || version != 2 {
		t.Fatal(version, err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO report_deliveries(topic_id,period_type,period_start,period_end,parts,message_ids)
	 SELECT id,'day','2026-10-07T00:00:00Z','2026-10-08T00:00:00Z',ARRAY['first','second'],ARRAY[123::bigint] FROM tracked_topics LIMIT 1`); err != nil {
		t.Fatal(err)
	}
	if err := migrations.Up(ctx, db); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM report_deliveries WHERE parts=ARRAY['first','second'] AND message_ids=ARRAY[123::bigint]`).Scan(&count); err != nil || count != 1 {
		t.Fatal(count, err)
	}
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM players WHERE dota_account_id=123456789`).Scan(&count); err != nil || count != 1 {
		t.Fatal(count, err)
	}
}

func TestDeliverySchemaStillValidated(t *testing.T) {
	for _, mutation := range []string{"DROP TABLE match_deliveries", "ALTER TABLE report_deliveries DROP COLUMN message_ids"} {
		t.Run(mutation, func(t *testing.T) {
			ctx, db, _ := testdb.Open(t, true)
			if _, err := db.ExecContext(ctx, mutation); err != nil {
				t.Fatal(err)
			}
			if err := migrations.Up(ctx, db); err == nil || !strings.Contains(err.Error(), "incompatible") {
				t.Fatal("accepted broken delivery schema", err)
			}
		})
	}
}
