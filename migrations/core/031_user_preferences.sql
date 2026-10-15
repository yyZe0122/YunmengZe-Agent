ALTER TABLE memory_entries ADD COLUMN preference_key TEXT NOT NULL DEFAULT '';
ALTER TABLE memory_entries ADD COLUMN evidence_json TEXT NOT NULL DEFAULT '[]';
CREATE UNIQUE INDEX memory_preference_key_idx ON memory_entries(preference_key) WHERE preference_key != '';
CREATE TABLE memory_curation_state (
 id INTEGER PRIMARY KEY CHECK(id=1),
 through_message_id INTEGER NOT NULL DEFAULT 0,
 attempted_at TEXT NOT NULL DEFAULT ''
);
INSERT INTO memory_curation_state(id) VALUES(1);
