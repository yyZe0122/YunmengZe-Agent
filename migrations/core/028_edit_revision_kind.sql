-- QG 1:1 rewind: distinguish create / modify / mkdir / delete (ADR-051).
ALTER TABLE edit_revisions ADD COLUMN kind TEXT NOT NULL DEFAULT 'modify';

-- Backfill pre-kind rows from checkpoint fingerprints.
-- create: no prior bytes, new content hash. mkdir: no prior bytes, no after hash.
-- Ambiguity: modifying an empty file looks like create; undo may delete instead of restore empty.
UPDATE edit_revisions SET kind = 'create'
 WHERE sha_before = '' AND artifact_id = '' AND sha_after != '';
UPDATE edit_revisions SET kind = 'mkdir'
 WHERE sha_before = '' AND artifact_id = '' AND sha_after = '';
