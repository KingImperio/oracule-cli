package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/KingImperio/oracule-cli/pkg/models"
	_ "modernc.org/sqlite"
)

const schemaVersion = 1

var migrateSQL = []string{
	`CREATE TABLE IF NOT EXISTS schema_version (
		version INTEGER NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS sessions (
		id            TEXT PRIMARY KEY,
		title         TEXT NOT NULL DEFAULT '',
		directory     TEXT NOT NULL DEFAULT '',
		created_at    TEXT NOT NULL,
		updated_at    TEXT NOT NULL,
		compacted_at  TEXT NOT NULL DEFAULT '',
		archived_at   TEXT NOT NULL DEFAULT '',
		model_id      TEXT NOT NULL DEFAULT '',
		agent_name    TEXT NOT NULL DEFAULT '',
		permissions   TEXT NOT NULL DEFAULT '{}',
		summary_adds  INTEGER NOT NULL DEFAULT 0,
		summary_dels  INTEGER NOT NULL DEFAULT 0,
		summary_files INTEGER NOT NULL DEFAULT 0,
		revert_to_msg TEXT NOT NULL DEFAULT '',
		revert_to_part TEXT NOT NULL DEFAULT '',
		revert_to_snap TEXT NOT NULL DEFAULT '',
		revert_to_diff TEXT NOT NULL DEFAULT ''
	)`,
	`CREATE TABLE IF NOT EXISTS messages (
		id          TEXT PRIMARY KEY,
		session_id  TEXT NOT NULL,
		role        TEXT NOT NULL,
		parts_json  TEXT NOT NULL DEFAULT '[]',
		timestamp   TEXT NOT NULL,
		cost_usd    REAL NOT NULL DEFAULT 0,
		model_id    TEXT NOT NULL DEFAULT '',
		token_in    INTEGER NOT NULL DEFAULT 0,
		token_out   INTEGER NOT NULL DEFAULT 0,
		stop_reason TEXT NOT NULL DEFAULT ''
	)`,
	`CREATE INDEX IF NOT EXISTS idx_messages_session ON messages(session_id, timestamp)`,
	`CREATE TABLE IF NOT EXISTS revert_points (
		message_id TEXT NOT NULL,
		session_id TEXT NOT NULL,
		part_id    TEXT NOT NULL DEFAULT '',
		snapshot   TEXT NOT NULL DEFAULT '',
		diff       TEXT NOT NULL DEFAULT '',
		PRIMARY KEY (message_id, session_id)
	)`,
}

// SQLiteStore is the SQLite-backed implementation of agent.SessionStore.
type SQLiteStore struct {
	db *sql.DB
}

// NewSQLite opens or creates a SQLite database at the given path.
func NewSQLite(dbPath string) (*SQLiteStore, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	s := &SQLiteStore{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return s, nil
}

func (s *SQLiteStore) migrate() error {
	for _, stmt := range migrateSQL {
		if _, err := s.db.Exec(stmt); err != nil {
			return fmt.Errorf("migrate stmt: %w", err)
		}
	}

	var ver int
	err := s.db.QueryRow("SELECT COALESCE(MAX(version), 0) FROM schema_version").Scan(&ver)
	if err != nil {
		_ = err
	}

	if ver < schemaVersion {
		_, _ = s.db.Exec("INSERT OR REPLACE INTO schema_version (version) VALUES (?)", schemaVersion)
	}

	return nil
}

// Close shuts down the database connection.
func (s *SQLiteStore) Close() error {
	return s.db.Close()
}

// ---------------------------------------------------------------------------
// Session operations
// ---------------------------------------------------------------------------

func (s *SQLiteStore) GetSession(_ context.Context, id string) (*models.Session, error) {
	row := s.db.QueryRow(
		`SELECT id, title, directory, created_at, updated_at, compacted_at, archived_at,
		        model_id, agent_name, permissions,
		        summary_adds, summary_dels, summary_files,
		        revert_to_msg, revert_to_part, revert_to_snap, revert_to_diff
		 FROM sessions WHERE id = ?`, id,
	)

	var sess models.Session
	var createdAt, updatedAt, compactedAt, archivedAt string
	var permsJSON string
	var revMsgID, revPartID, revSnap, revDiff string

	err := row.Scan(
		&sess.ID, &sess.Title, &sess.Directory,
		&createdAt, &updatedAt, &compactedAt, &archivedAt,
		&sess.ModelID, &sess.AgentName, &permsJSON,
		&sess.SummaryAdds, &sess.SummaryDels, &sess.SummaryFiles,
		&revMsgID, &revPartID, &revSnap, &revDiff,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scan session: %w", err)
	}

	sess.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	sess.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
	if compactedAt != "" {
		sess.CompactedAt, _ = time.Parse(time.RFC3339, compactedAt)
	}
	if archivedAt != "" {
		sess.ArchivedAt, _ = time.Parse(time.RFC3339, archivedAt)
	}

	_ = json.Unmarshal([]byte(permsJSON), &sess.Permission)

	if revMsgID != "" {
		sess.RevertTo = &models.RevertPoint{
			MessageID: revMsgID,
			PartID:    revPartID,
			Snapshot:  revSnap,
			Diff:      revDiff,
		}
	}

	return &sess, nil
}

func (s *SQLiteStore) SaveSession(_ context.Context, sess *models.Session) error {
	if sess == nil {
		return errors.New("session is nil")
	}

	permsJSON, _ := json.Marshal(sess.Permission)

	var revMsgID, revPartID, revSnap, revDiff string
	if sess.RevertTo != nil {
		revMsgID = sess.RevertTo.MessageID
		revPartID = sess.RevertTo.PartID
		revSnap = sess.RevertTo.Snapshot
		revDiff = sess.RevertTo.Diff
	}

	_, err := s.db.Exec(
		`INSERT OR REPLACE INTO sessions
		 (id, title, directory, created_at, updated_at, compacted_at, archived_at,
		  model_id, agent_name, permissions,
		  summary_adds, summary_dels, summary_files,
		  revert_to_msg, revert_to_part, revert_to_snap, revert_to_diff)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		sess.ID, sess.Title, sess.Directory,
		formatTime(sess.CreatedAt), formatTime(sess.UpdatedAt),
		formatTime(sess.CompactedAt), formatTime(sess.ArchivedAt),
		sess.ModelID, sess.AgentName, string(permsJSON),
		sess.SummaryAdds, sess.SummaryDels, sess.SummaryFiles,
		revMsgID, revPartID, revSnap, revDiff,
	)
	return err
}

// ---------------------------------------------------------------------------
// Message operations
// ---------------------------------------------------------------------------

func (s *SQLiteStore) AppendMessage(_ context.Context, m models.Message) error {
	if m.ID == "" {
		return errors.New("message ID is required")
	}
	if m.SessionID == "" {
		return errors.New("session ID is required")
	}

	partsJSON, _ := json.Marshal(m.Parts)

	_, err := s.db.Exec(
		`INSERT INTO messages
		 (id, session_id, role, parts_json, timestamp, cost_usd, model_id, token_in, token_out, stop_reason)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		m.ID, m.SessionID, string(m.Role), string(partsJSON),
		formatTime(m.Timestamp), m.CostUSD, m.ModelID,
		m.TokenIn, m.TokenOut, m.StopReason,
	)
	return err
}

func (s *SQLiteStore) AppendPart(ctx context.Context, sessionID, messageID string, p models.Part) error {
	// Fetch existing parts, append, re-save
	var partsJSON string
	err := s.db.QueryRow("SELECT parts_json FROM messages WHERE id = ?", messageID).Scan(&partsJSON)
	if err != nil {
		return fmt.Errorf("get message for append: %w", err)
	}

	var parts []models.Part
	_ = json.Unmarshal([]byte(partsJSON), &parts)
	parts = append(parts, p)

	newJSON, _ := json.Marshal(parts)
	_, err = s.db.Exec("UPDATE messages SET parts_json = ? WHERE id = ?", string(newJSON), messageID)
	return err
}

func (s *SQLiteStore) GetMessages(_ context.Context, sessionID string, limit int) ([]models.Message, error) {
	if limit <= 0 {
		limit = 200
	}

	rows, err := s.db.Query(
		`SELECT id, role, parts_json, timestamp, cost_usd, model_id, token_in, token_out, stop_reason
		 FROM messages WHERE session_id = ?
		 ORDER BY timestamp ASC
		 LIMIT ?`, sessionID, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("query messages: %w", err)
	}
	defer rows.Close()

	var msgs []models.Message
	for rows.Next() {
		var m models.Message
		var roleStr, partsJSON, ts string
		var stopReason string

		err := rows.Scan(&m.ID, &roleStr, &partsJSON, &ts, &m.CostUSD, &m.ModelID,
			&m.TokenIn, &m.TokenOut, &stopReason)
		if err != nil {
			return nil, fmt.Errorf("scan message: %w", err)
		}

		m.Role = models.MessageRole(roleStr)
		m.StopReason = stopReason
		m.Timestamp, _ = time.Parse(time.RFC3339, ts)

		var parts []models.Part
		if partsJSON != "" {
			_ = json.Unmarshal([]byte(partsJSON), &parts)
		}
		m.Parts = parts

		msgs = append(msgs, m)
	}

	return msgs, nil
}

// ---------------------------------------------------------------------------
// Revert point operations
// ---------------------------------------------------------------------------

func (s *SQLiteStore) CreateRevertPoint(_ context.Context, rp models.RevertPoint) error {
	_, err := s.db.Exec(
		`INSERT OR REPLACE INTO revert_points (message_id, session_id, part_id, snapshot, diff)
		 VALUES (?, ?, ?, ?, ?)`,
		rp.MessageID, rp.MessageID, rp.PartID, rp.Snapshot, rp.Diff,
	)
	return err
}

func (s *SQLiteStore) GetRevertPoint(_ context.Context, sessionID, messageID string) (*models.RevertPoint, error) {
	var rp models.RevertPoint
	err := s.db.QueryRow(
		`SELECT message_id, part_id, snapshot, diff FROM revert_points
		 WHERE message_id = ? AND session_id = ?`,
		messageID, sessionID,
	).Scan(&rp.MessageID, &rp.PartID, &rp.Snapshot, &rp.Diff)

	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scan revert point: %w", err)
	}
	return &rp, nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339)
}
