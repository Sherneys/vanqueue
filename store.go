package main

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

type Settings struct {
	Date   Day // วันที่จะจัดรถ
	Needed int // จำนวนรถตู้ที่ต้องการใช้งานต่อวัน
}

type Van struct {
	ID         int64
	Seq        int
	Code       string
	Driver     string
	Plate      string
	Phone      string
	Seats      int
	Note       string
	Zone       string
	Status     string
	StartDate  Day
	LeaveStart Day
	LeaveEnd   Day
}

type RunLog struct {
	ID          int64
	Date        Day
	Code        string
	Destination string
	Status      string
	Note        string
}

// Store keeps the data in Postgres (Supabase). Tables are created on start-up.
type Store struct{ db *sql.DB }

const schema = `
CREATE TABLE IF NOT EXISTS settings (key text PRIMARY KEY, value text NOT NULL);
CREATE TABLE IF NOT EXISTS vans (
	id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
	seq integer NOT NULL,
	code text NOT NULL UNIQUE,
	driver text NOT NULL,
	plate text NOT NULL DEFAULT '',
	phone text NOT NULL DEFAULT '',
	seats integer NOT NULL DEFAULT 0,
	note text NOT NULL DEFAULT '',
	zone text NOT NULL DEFAULT '',
	status text NOT NULL DEFAULT 'ใช้งานได้',
	start_date date,
	leave_start date,
	leave_end date
);
CREATE TABLE IF NOT EXISTS run_logs (
	id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
	date date,
	code text NOT NULL,
	destination text NOT NULL DEFAULT '',
	status text NOT NULL DEFAULT 'วิ่งแล้ว',
	note text NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS run_logs_date ON run_logs(date);
-- The app connects as the database owner. Enabling RLS without policies keeps these
-- tables closed to Supabase's public REST API (anon / authenticated keys).
ALTER TABLE settings ENABLE ROW LEVEL SECURITY;
ALTER TABLE vans ENABLE ROW LEVEL SECURITY;
ALTER TABLE run_logs ENABLE ROW LEVEL SECURITY;
`

func OpenStore(databaseURL string) (*Store, error) {
	cfg, err := pgx.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("DATABASE_URL: %w", err)
	}
	// Simple protocol works through Supabase's transaction pooler (port 6543),
	// which does not support prepared statements.
	cfg.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	db := stdlib.OpenDB(*cfg)
	db.SetMaxOpenConns(5)
	db.SetConnMaxIdleTime(5 * time.Minute)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("connect: %w", err)
	}
	if _, err := db.ExecContext(ctx, schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("create schema: %w", err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

// dateArg turns a Day into a query argument (NULL when empty).
func dateArg(d Day) any {
	if d == 0 {
		return nil
	}
	return d.ISO()
}

func dayFrom(t sql.NullTime) Day {
	if !t.Valid {
		return 0
	}
	return DayOf(t.Time)
}

func (s *Store) Settings() (Settings, error) {
	st := Settings{Date: Today(), Needed: 50}
	rows, err := s.db.Query(`SELECT key, value FROM settings`)
	if err != nil {
		return st, err
	}
	defer rows.Close()
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return st, err
		}
		switch k {
		case "date":
			if d, err := ParseDay(v); err == nil && d != 0 {
				st.Date = d
			}
		case "needed":
			if n, err := strconv.Atoi(v); err == nil {
				st.Needed = n
			}
		}
	}
	return st, rows.Err()
}

func (s *Store) SaveSettings(st Settings) error {
	return saveSettings(s.db, st)
}

type execer interface {
	Exec(string, ...any) (sql.Result, error)
	QueryRow(string, ...any) *sql.Row
}

func saveSettings(db execer, st Settings) error {
	for k, v := range map[string]string{"date": st.Date.ISO(), "needed": strconv.Itoa(st.Needed)} {
		if _, err := db.Exec(`INSERT INTO settings(key, value) VALUES($1, $2)
			ON CONFLICT(key) DO UPDATE SET value = excluded.value`, k, v); err != nil {
			return err
		}
	}
	return nil
}

const vanCols = `id, seq, code, driver, plate, phone, seats, note, zone, status, start_date, leave_start, leave_end`

func scanVan(sc interface{ Scan(...any) error }) (Van, error) {
	var v Van
	var start, ls, le sql.NullTime
	err := sc.Scan(&v.ID, &v.Seq, &v.Code, &v.Driver, &v.Plate, &v.Phone, &v.Seats, &v.Note, &v.Zone, &v.Status, &start, &ls, &le)
	v.StartDate, v.LeaveStart, v.LeaveEnd = dayFrom(start), dayFrom(ls), dayFrom(le)
	return v, err
}

// Vans returns all vans in list order; that order breaks queue ties like sheet rows do.
func (s *Store) Vans() ([]Van, error) {
	rows, err := s.db.Query(`SELECT ` + vanCols + ` FROM vans ORDER BY seq, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Van
	for rows.Next() {
		v, err := scanVan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *Store) Van(id int64) (Van, error) {
	return scanVan(s.db.QueryRow(`SELECT `+vanCols+` FROM vans WHERE id = $1`, id))
}

func vanArgs(v *Van) []any {
	return []any{v.Code, v.Driver, v.Plate, v.Phone, v.Seats, v.Note, v.Zone, v.Status,
		dateArg(v.StartDate), dateArg(v.LeaveStart), dateArg(v.LeaveEnd)}
}

func (s *Store) SaveVan(v *Van) error {
	if v.ID == 0 {
		return insertVan(s.db, v)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var oldCode string
	if err := tx.QueryRow(`SELECT code FROM vans WHERE id = $1 FOR UPDATE`, v.ID).Scan(&oldCode); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE vans SET code=$1, driver=$2, plate=$3, phone=$4, seats=$5, note=$6, zone=$7, status=$8,
		start_date=$9, leave_start=$10, leave_end=$11 WHERE id=$12`, append(vanArgs(v), v.ID)...); err != nil {
		return err
	}
	// Renaming a van keeps its run history (and so its place in the queue).
	if oldCode != v.Code {
		if _, err := tx.Exec(`UPDATE run_logs SET code = $1 WHERE code = $2`, v.Code, oldCode); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func insertVan(db execer, v *Van) error {
	var seq any // nil appends to the end of the list
	if v.Seq != 0 {
		seq = v.Seq
	}
	return db.QueryRow(`INSERT INTO vans(seq, code, driver, plate, phone, seats, note, zone, status, start_date, leave_start, leave_end)
		VALUES(COALESCE($1::integer, (SELECT COALESCE(MAX(seq), 0) + 1 FROM vans)), $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		RETURNING id`, append([]any{seq}, vanArgs(v)...)...).Scan(&v.ID)
}

func (s *Store) DeleteVan(id int64) error {
	_, err := s.db.Exec(`DELETE FROM vans WHERE id = $1`, id)
	return err
}

const logCols = `id, date, code, destination, status, note`

func scanLog(sc interface{ Scan(...any) error }) (RunLog, error) {
	var l RunLog
	var date sql.NullTime
	err := sc.Scan(&l.ID, &date, &l.Code, &l.Destination, &l.Status, &l.Note)
	l.Date = dayFrom(date)
	return l, err
}

func (s *Store) Logs() ([]RunLog, error) {
	rows, err := s.db.Query(`SELECT ` + logCols + ` FROM run_logs ORDER BY date NULLS FIRST, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RunLog
	for rows.Next() {
		l, err := scanLog(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

func (s *Store) Log(id int64) (RunLog, error) {
	return scanLog(s.db.QueryRow(`SELECT `+logCols+` FROM run_logs WHERE id = $1`, id))
}

func (s *Store) AddLogs(logs []RunLog) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := insertLogs(tx, logs); err != nil {
		return err
	}
	return tx.Commit()
}

// insertLogs writes rows in batches so a big import is a few round trips, not thousands.
func insertLogs(db execer, logs []RunLog) error {
	const batch = 500
	for start := 0; start < len(logs); start += batch {
		chunk := logs[start:min(start+batch, len(logs))]
		var q strings.Builder
		q.WriteString(`INSERT INTO run_logs(date, code, destination, status, note) VALUES `)
		args := make([]any, 0, len(chunk)*5)
		for i, l := range chunk {
			if i > 0 {
				q.WriteString(", ")
			}
			n := i * 5
			fmt.Fprintf(&q, "($%d::date, $%d, $%d, $%d, $%d)", n+1, n+2, n+3, n+4, n+5)
			args = append(args, dateArg(l.Date), l.Code, l.Destination, l.Status, l.Note)
		}
		if _, err := db.Exec(q.String(), args...); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) SaveLog(l RunLog) error {
	_, err := s.db.Exec(`UPDATE run_logs SET date=$1, code=$2, destination=$3, status=$4, note=$5 WHERE id=$6`,
		dateArg(l.Date), l.Code, l.Destination, l.Status, l.Note, l.ID)
	return err
}

func (s *Store) DeleteLog(id int64) error {
	_, err := s.db.Exec(`DELETE FROM run_logs WHERE id = $1`, id)
	return err
}

// ReplaceAll wipes every table and loads the given data (used by the sheet import).
func (s *Store) ReplaceAll(st Settings, vans []Van, logs []RunLog) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`TRUNCATE settings, vans, run_logs RESTART IDENTITY`); err != nil {
		return err
	}
	if err := saveSettings(tx, st); err != nil {
		return err
	}
	for i := range vans {
		vans[i].Seq = i + 1
		if err := insertVan(tx, &vans[i]); err != nil {
			return fmt.Errorf("van %s: %w", vans[i].Code, err)
		}
	}
	if err := insertLogs(tx, logs); err != nil {
		return err
	}
	return tx.Commit()
}
