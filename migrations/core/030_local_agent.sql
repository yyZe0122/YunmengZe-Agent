-- ADR-057: local session engine. Released migrations 001-029 remain immutable.
CREATE TABLE chat_sessions (
 session_id TEXT PRIMARY KEY,
 parent_id TEXT NOT NULL DEFAULT '',
 kind TEXT NOT NULL DEFAULT 'general',
 title TEXT NOT NULL DEFAULT '',
 workspace TEXT NOT NULL,
 mode TEXT NOT NULL DEFAULT 'agent' CHECK(mode IN ('agent','plan')),
 model TEXT NOT NULL DEFAULT '',
 summary TEXT NOT NULL DEFAULT '',
 summary_through INTEGER NOT NULL DEFAULT 0,
 lease_owner TEXT NOT NULL DEFAULT '',
 lease_until TEXT NOT NULL DEFAULT '',
 created_at TEXT NOT NULL,
 updated_at TEXT NOT NULL
);
CREATE INDEX chat_sessions_recent ON chat_sessions(parent_id, updated_at DESC);
CREATE TABLE chat_messages (
 message_id INTEGER PRIMARY KEY AUTOINCREMENT,
 session_id TEXT NOT NULL REFERENCES chat_sessions(session_id),
 message TEXT NOT NULL CHECK(json_valid(message)),
 usage TEXT NOT NULL DEFAULT '{}' CHECK(json_valid(usage)),
 created_at TEXT NOT NULL
);
CREATE INDEX chat_messages_session ON chat_messages(session_id,message_id);

INSERT INTO chat_sessions(session_id,title,workspace,mode,model,created_at,updated_at)
SELECT session_id, '', COALESCE(json_extract(metadata,'$.workspace'),''),
 CASE WHEN COALESCE(NULLIF(json_extract(metadata,'$.permission_stance'),''),
  (SELECT execution_mode FROM tasks WHERE tasks.session_id=sessions.session_id ORDER BY created_at DESC,task_id DESC LIMIT 1),'agent')='plan'
 THEN 'plan' ELSE 'agent' END,
 COALESCE(json_extract(metadata,'$.model'),''),created_at,updated_at FROM sessions;
INSERT INTO chat_sessions(session_id,parent_id,kind,workspace,mode,model,created_at,updated_at)
SELECT 'child_'||r.run_id,t.session_id,'general',s.workspace,s.mode,s.model,r.started_at,COALESCE(r.finished_at,r.started_at)
FROM runs r JOIN tasks t ON t.task_id=r.task_id JOIN chat_sessions s ON s.session_id=t.session_id
WHERE r.parent_run_id IS NOT NULL;

-- Each old run repeats its full input prefix. Copy only its last initial user
-- message, subsequent steering messages, and new assistant/tool records.
INSERT INTO chat_messages(session_id,message,usage,created_at)
SELECT CASE WHEN r.parent_run_id IS NULL THEN t.session_id ELSE 'child_'||r.run_id END,
 a.message,a.usage,a.created_at
FROM agent_run_records a JOIN runs r ON r.run_id=a.run_id JOIN tasks t ON t.task_id=r.task_id
WHERE json_extract(a.message,'$.role')<>'system'
 AND NOT EXISTS (
  SELECT 1 FROM sessions old_session,json_each(old_session.metadata,'$.hidden_task_ids') hidden
  WHERE old_session.session_id=t.session_id AND hidden.value=t.task_id
 )
 AND (a.record_type<>'input_message' OR a.position >= (
  SELECT MAX(b.position) FROM agent_run_records b WHERE b.run_id=a.run_id AND b.record_type='input_message'
   AND b.position < COALESCE((SELECT MIN(c.position) FROM agent_run_records c WHERE c.run_id=a.run_id AND c.record_type='assistant_message'),2147483647)
 ))
ORDER BY r.started_at,r.run_id,a.position;

DROP TABLE IF EXISTS job_leases;
DROP TABLE IF EXISTS job_runs;
DROP TABLE IF EXISTS jobs;
DROP TABLE IF EXISTS task_skill_snapshots;
DROP TABLE IF EXISTS tool_calls;
DROP TABLE IF EXISTS agent_run_records;
UPDATE runs SET parent_run_id=NULL;
DROP TABLE IF EXISTS runs;
DROP TABLE IF EXISTS evolution_activation_applications;
DROP TABLE IF EXISTS evolution_activation_authorizations;
DROP TABLE IF EXISTS capability_grants;
DROP TABLE IF EXISTS approvals;
DROP TABLE IF EXISTS plan_steps;
DROP TABLE IF EXISTS plans;
DROP TABLE IF EXISTS tasks;
DROP TABLE IF EXISTS sessions;
DROP TABLE IF EXISTS tool_permission_requests;
DROP TABLE IF EXISTS user_questions;
DROP TABLE IF EXISTS session_todos;
DROP TABLE IF EXISTS edit_revisions;
DROP TABLE IF EXISTS skill_usage;
DROP TABLE IF EXISTS skill_events;
DROP TABLE IF EXISTS context_snapshots;
DROP TABLE IF EXISTS session_compactions;
DROP TABLE IF EXISTS transcript_search;
DROP TABLE IF EXISTS transcript_fts;
DROP TABLE IF EXISTS events;
DROP TABLE IF EXISTS audit_log;
DROP TABLE IF EXISTS artifacts;
