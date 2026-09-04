-- ADR-039: persist child kind/tools so task_id resume can fail-closed.
ALTER TABLE runs ADD COLUMN child_kind TEXT;
ALTER TABLE runs ADD COLUMN child_tools TEXT;
