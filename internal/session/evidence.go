package session

import "context"

// UserEvidence contains direct user text from root agent sessions, including
// answers to ask_user. Model output and skill/command expansions are excluded.
type UserEvidence struct {
	ID                       int64
	Session, Text, CreatedAt string
}

func (s *Store) UserEvidence(ctx context.Context, through int64) (previous, current []UserEvidence, err error) {
	current, err = s.userEvidence(ctx, `m.message_id>? ORDER BY m.message_id LIMIT 64`, through)
	if err != nil {
		return nil, nil, err
	}
	previous, err = s.userEvidence(ctx, `m.message_id<=? ORDER BY m.message_id DESC LIMIT 16`, through)
	if err != nil {
		return nil, nil, err
	}
	for i, j := 0, len(previous)-1; i < j; i, j = i+1, j-1 {
		previous[i], previous[j] = previous[j], previous[i]
	}
	return previous, current, nil
}

func (s *Store) userEvidence(ctx context.Context, window string, through int64) ([]UserEvidence, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT m.message_id,m.session_id,json_extract(m.message,'$.user_text'),m.created_at FROM chat_messages m JOIN chat_sessions s ON s.session_id=m.session_id WHERE s.parent_id='' AND s.mode='agent' AND json_extract(m.message,'$.role') IN ('user','tool') AND json_extract(m.message,'$.user_text') IS NOT NULL AND `+window, through)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var samples []UserEvidence
	for rows.Next() {
		var sample UserEvidence
		if err := rows.Scan(&sample.ID, &sample.Session, &sample.Text, &sample.CreatedAt); err != nil {
			return nil, err
		}
		samples = append(samples, sample)
	}
	return samples, rows.Err()
}
