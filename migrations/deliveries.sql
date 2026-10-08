-- Stable contents and acknowledged parts of unfinished worker deliveries.
CREATE TABLE report_deliveries (
    id BIGSERIAL PRIMARY KEY,
    topic_id INTEGER NOT NULL REFERENCES tracked_topics(id) ON DELETE CASCADE,
    period_type TEXT NOT NULL CHECK (period_type IN ('day','week','month')),
    period_start TIMESTAMPTZ NOT NULL,
    period_end TIMESTAMPTZ NOT NULL CHECK (period_end > period_start),
    parts TEXT[],
    message_ids BIGINT[] NOT NULL DEFAULT '{}',
    UNIQUE(topic_id,period_type,period_start,period_end),
    CHECK (cardinality(message_ids) <= coalesce(cardinality(parts),0))
);
CREATE TABLE match_deliveries (
    topic_id INTEGER NOT NULL REFERENCES tracked_topics(id) ON DELETE CASCADE,
    match_id BIGINT NOT NULL CHECK (match_id > 0),
    player_ids BIGINT[] NOT NULL CHECK (cardinality(player_ids) > 0),
    parts TEXT[] NOT NULL CHECK (cardinality(parts) > 0),
    message_ids BIGINT[] NOT NULL DEFAULT '{}',
    PRIMARY KEY(topic_id,match_id),
    CHECK (cardinality(message_ids) <= cardinality(parts))
);
