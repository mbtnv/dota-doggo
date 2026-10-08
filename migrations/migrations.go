// Package migrations bootstraps PostgreSQL or adopts the compatible legacy schema.
package migrations

import (
	"context"
	"crypto/rand"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"
)

const LegacyRevision = "20260505_000004"
const CurrentVersion int64 = 2

//go:embed schema.sql
var schemaSQL string

//go:embed deliveries.sql
var deliveriesSQL string

var tables = []string{"tracked_topics", "players", "topic_players", "player_matches", "report_runs", "topic_runtime_state", "constant_entries", "alembic_version"}

// Open creates a dedicated SQL pool, independent of the application's pgx pool.
func Open(ctx context.Context, dsn string) (*sql.DB, error) {
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		return nil, errors.New("invalid PostgreSQL connection configuration")
	}
	db := stdlib.OpenDB(*cfg)
	db.SetMaxOpenConns(2)
	db.SetMaxIdleConns(2)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("connect to PostgreSQL: %w", err)
	}
	return db, nil
}

func provider(db *sql.DB) (*goose.Provider, error) {
	locker, err := lock.NewPostgresSessionLocker()
	if err != nil {
		return nil, err
	}
	return goose.NewProvider(goose.DialectPostgres, db, nil,
		goose.WithDisableGlobalRegistry(true), goose.WithSessionLocker(locker),
		goose.WithGoMigrations(goose.NewGoMigration(1, &goose.GoFunc{RunTx: baseline},
			&goose.GoFunc{RunTx: func(context.Context, *sql.Tx) error {
				return errors.New("baseline cannot be dropped; restore a verified backup instead")
			}}), goose.NewGoMigration(2, &goose.GoFunc{RunTx: func(ctx context.Context, tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, deliveriesSQL)
			return err
		}}, &goose.GoFunc{RunTx: func(context.Context, *sql.Tx) error {
			return errors.New("delivery progress cannot be dropped; restore a verified backup instead")
		}})))
}

// Up validates already-versioned schemas too: a version marker alone is insufficient.
func Up(ctx context.Context, db *sql.DB) error {
	version, err := Version(ctx, db)
	if err != nil {
		return err
	}
	if version > CurrentVersion {
		return fmt.Errorf("database version %d is newer than this binary supports (%d)", version, CurrentVersion)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	count, err := existingCount(ctx, tx)
	if err != nil || (version > 0 && count == 0) {
		_ = tx.Rollback()
		if err != nil {
			return err
		}
		return errors.New("versioned database is missing the application schema")
	}
	if err := validateExisting(ctx, tx, version); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Rollback(); err != nil {
		return err
	}
	p, err := provider(db)
	if err != nil {
		return err
	}
	_, err = p.Up(ctx)
	return err
}

func Version(ctx context.Context, db *sql.DB) (int64, error) {
	var exists bool
	if err := db.QueryRowContext(ctx, "SELECT to_regclass('goose_db_version') IS NOT NULL").Scan(&exists); err != nil {
		return 0, err
	}
	if !exists {
		return 0, nil
	}
	p, err := provider(db)
	if err != nil {
		return 0, err
	}
	return p.GetDBVersion(ctx)
}

func baseline(ctx context.Context, tx *sql.Tx) error {
	count, err := existingCount(ctx, tx)
	if err != nil {
		return err
	}
	if count == 0 {
		_, err := tx.ExecContext(ctx, schemaSQL)
		return err
	}
	return validateExisting(ctx, tx, 1)
}

func existingCount(ctx context.Context, tx *sql.Tx) (int, error) {
	var count int
	err := tx.QueryRowContext(ctx, `SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
		WHERE n.nspname=current_schema() AND c.relname=ANY($1) AND c.relkind IN ('r','p')`, tables).Scan(&count)
	return count, err
}

func validateExisting(ctx context.Context, tx *sql.Tx, version int64) error {
	count, err := existingCount(ctx, tx)
	if err != nil || count == 0 {
		return err
	}
	if count != len(tables) {
		return errors.New("incomplete application schema; migrate the Python database to revision " + LegacyRevision + " first")
	}
	var revision string
	var revisions int
	if err := tx.QueryRowContext(ctx, "SELECT count(*), coalesce(min(version_num),'') FROM alembic_version").Scan(&revisions, &revision); err != nil {
		return err
	}
	if revisions != 1 || revision != LegacyRevision {
		return fmt.Errorf("unsupported Alembic revision; expected %s", LegacyRevision)
	}
	var original, searchPath string
	if err := tx.QueryRowContext(ctx, "SELECT current_schema(), current_setting('search_path')").Scan(&original, &searchPath); err != nil {
		return err
	}
	selectedTables := append([]string(nil), tables...)
	if version >= 2 {
		selectedTables = append(selectedTables, "report_deliveries", "match_deliveries")
	}
	actual, err := snapshot(ctx, tx, original, selectedTables)
	if err != nil {
		return err
	}
	name := "doggo_expected_" + fmt.Sprintf("%x", randomID())
	quoted := pgx.Identifier{name}.Sanitize()
	if _, err := tx.ExecContext(ctx, "CREATE SCHEMA "+quoted); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "SELECT set_config('search_path',$1,true)", quoted); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, schemaSQL); err != nil {
		return err
	}
	if version >= 2 {
		if _, err := tx.ExecContext(ctx, deliveriesSQL); err != nil {
			return err
		}
	}
	expected, err := snapshot(ctx, tx, name, selectedTables)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "SELECT set_config('search_path',$1,true)", searchPath); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "DROP SCHEMA "+quoted+" CASCADE"); err != nil {
		return err
	}
	var missing []string
	for item := range expected {
		if !actual[item] {
			missing = append(missing, item)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return fmt.Errorf("incompatible PostgreSQL schema: expected %s", missing[0])
	}
	// Extra non-unique indexes do not restrict writes. Unreviewed columns,
	// constraints and unique indexes may change INSERT semantics, so refuse them.
	for item := range actual {
		if strings.HasPrefix(item, "column|") && !expected[item] {
			return fmt.Errorf("unreviewed column definition: %s", item)
		}
		if strings.HasPrefix(item, "constraint|") && !expected[item] {
			return fmt.Errorf("unreviewed constraint: %s", item)
		}
		if strings.HasPrefix(item, "index|unique|") && !expected[item] {
			return fmt.Errorf("unreviewed unique index: %s", item)
		}
	}
	return nil
}

func randomID() []byte {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return b
}

// snapshot compares semantics rather than IDs, sequence counters or constraint names.
func snapshot(ctx context.Context, tx *sql.Tx, schema string, selectedTables []string) (map[string]bool, error) {
	queries := []string{
		`SELECT 'column|'||c.relname||'|'||a.attname||'|'||format_type(a.atttypid,a.atttypmod)||'|'||a.attnotnull::text||'|'||coalesce(pg_get_expr(d.adbin,d.adrelid),'')
		 FROM pg_attribute a JOIN pg_class c ON c.oid=a.attrelid JOIN pg_namespace n ON n.oid=c.relnamespace
		 LEFT JOIN pg_attrdef d ON d.adrelid=c.oid AND d.adnum=a.attnum
		 WHERE n.nspname=$1 AND c.relname=ANY($2) AND a.attnum>0 AND NOT a.attisdropped`,
		`SELECT 'constraint|'||c.relname||'|'||pg_get_constraintdef(k.oid,true)
		 FROM pg_constraint k JOIN pg_class c ON c.oid=k.conrelid JOIN pg_namespace n ON n.oid=c.relnamespace
		 WHERE n.nspname=$1 AND c.relname=ANY($2)`,
		`SELECT 'index|'||CASE WHEN x.indisunique THEN 'unique' ELSE 'regular' END||'|'||c.relname||'|'||x.indisprimary::text||'|'||x.indnullsnotdistinct::text||'|'||am.amname||'|'||
		 array_to_string(ARRAY(SELECT pg_get_indexdef(x.indexrelid,k,true) FROM generate_series(1,x.indnatts) k),'|')||'|'||coalesce(pg_get_expr(x.indpred,x.indrelid,true),'')
		 FROM pg_index x JOIN pg_class c ON c.oid=x.indrelid JOIN pg_class i ON i.oid=x.indexrelid JOIN pg_namespace n ON n.oid=c.relnamespace JOIN pg_am am ON am.oid=i.relam
		 WHERE n.nspname=$1 AND c.relname=ANY($2) AND x.indisvalid AND x.indisready`,
	}
	items := map[string]bool{}
	for _, query := range queries {
		rows, err := tx.QueryContext(ctx, query, schema, selectedTables)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var item string
			if err := rows.Scan(&item); err != nil {
				_ = rows.Close()
				return nil, err
			}
			item = strings.ReplaceAll(item, schema+".", "")
			item = strings.ReplaceAll(item, pgx.Identifier{schema}.Sanitize()+".", "")
			items[item] = true
		}
		err = rows.Err()
		_ = rows.Close()
		if err != nil {
			return nil, err
		}
	}
	return items, nil
}
