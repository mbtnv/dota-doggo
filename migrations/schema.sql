-- Generated from the four original Alembic upgrades; review changes.
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
