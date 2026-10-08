-- Anonymous legacy fixture exported from Python migrations.
CREATE TABLE tracked_topics (
    id SERIAL NOT NULL,
    telegram_chat_id BIGINT NOT NULL,
    telegram_thread_id BIGINT,
    title VARCHAR(255),
    timezone VARCHAR(64) NOT NULL,
    is_paused BOOLEAN DEFAULT false NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL,
    PRIMARY KEY (id),
    CONSTRAINT uq_tracked_topics_chat_thread UNIQUE (telegram_chat_id, telegram_thread_id)
);

CREATE INDEX ix_tracked_topics_telegram_chat_id ON tracked_topics (telegram_chat_id);

CREATE INDEX ix_tracked_topics_telegram_thread_id ON tracked_topics (telegram_thread_id);

CREATE TABLE players (
    id SERIAL NOT NULL,
    dota_account_id BIGINT NOT NULL,
    display_name VARCHAR(255) NOT NULL,
    profile_url TEXT,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL,
    PRIMARY KEY (id),
    UNIQUE (dota_account_id)
);

CREATE INDEX ix_players_dota_account_id ON players (dota_account_id);

CREATE TABLE topic_players (
    id SERIAL NOT NULL,
    topic_id INTEGER NOT NULL,
    player_id INTEGER NOT NULL,
    alias VARCHAR(255),
    last_seen_match_id BIGINT,
    added_by_telegram_user_id BIGINT,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL,
    PRIMARY KEY (id),
    CONSTRAINT uq_topic_players_topic_player UNIQUE (topic_id, player_id),
    FOREIGN KEY(topic_id) REFERENCES tracked_topics (id) ON DELETE CASCADE,
    FOREIGN KEY(player_id) REFERENCES players (id) ON DELETE CASCADE
);

CREATE TABLE player_matches (
    id SERIAL NOT NULL,
    player_id INTEGER NOT NULL,
    match_id BIGINT NOT NULL,
    start_time TIMESTAMP WITH TIME ZONE NOT NULL,
    end_time TIMESTAMP WITH TIME ZONE NOT NULL,
    hero_id INTEGER NOT NULL,
    radiant_win BOOLEAN NOT NULL,
    player_slot INTEGER NOT NULL,
    kills INTEGER NOT NULL,
    deaths INTEGER NOT NULL,
    assists INTEGER NOT NULL,
    gpm INTEGER NOT NULL,
    xpm INTEGER NOT NULL,
    hero_damage INTEGER NOT NULL,
    tower_damage INTEGER NOT NULL,
    hero_healing INTEGER NOT NULL,
    last_hits INTEGER NOT NULL,
    game_mode INTEGER NOT NULL,
    lobby_type INTEGER NOT NULL,
    party_size INTEGER,
    raw_payload JSON NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL,
    PRIMARY KEY (id),
    CONSTRAINT uq_player_matches_player_match UNIQUE (player_id, match_id),
    FOREIGN KEY(player_id) REFERENCES players (id) ON DELETE CASCADE
);

CREATE INDEX ix_player_matches_start_time ON player_matches (start_time);

CREATE INDEX ix_player_matches_end_time ON player_matches (end_time);

CREATE TABLE report_runs (
    id SERIAL NOT NULL,
    topic_id INTEGER NOT NULL,
    period_type VARCHAR(16) NOT NULL,
    period_start TIMESTAMP WITH TIME ZONE NOT NULL,
    period_end TIMESTAMP WITH TIME ZONE NOT NULL,
    trigger_source VARCHAR(32) NOT NULL,
    telegram_message_id BIGINT,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL,
    PRIMARY KEY (id),
    FOREIGN KEY(topic_id) REFERENCES tracked_topics (id) ON DELETE CASCADE
);

CREATE TABLE topic_runtime_state (
    id SERIAL NOT NULL,
    topic_id INTEGER NOT NULL,
    last_poll_started_at TIMESTAMP WITH TIME ZONE,
    last_poll_finished_at TIMESTAMP WITH TIME ZONE,
    last_poll_succeeded_at TIMESTAMP WITH TIME ZONE,
    last_poll_error TEXT,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL,
    PRIMARY KEY (id),
    UNIQUE (topic_id),
    FOREIGN KEY(topic_id) REFERENCES tracked_topics (id) ON DELETE CASCADE
);

CREATE TABLE constant_entries (
    id SERIAL NOT NULL,
    resource VARCHAR(32) NOT NULL,
    code INTEGER NOT NULL,
    name VARCHAR(255) NOT NULL,
    raw_payload JSON NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL,
    PRIMARY KEY (id),
    CONSTRAINT uq_constant_entries_resource_code UNIQUE (resource, code)
);

CREATE UNIQUE INDEX uq_tracked_topics_chat_null_thread ON tracked_topics (telegram_chat_id) WHERE telegram_thread_id IS NULL;


CREATE TABLE alembic_version (
	version_num VARCHAR(32) NOT NULL,
	CONSTRAINT alembic_version_pkc PRIMARY KEY (version_num)
)

;
INSERT INTO alembic_version (version_num) VALUES ('20260505_000004');

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
