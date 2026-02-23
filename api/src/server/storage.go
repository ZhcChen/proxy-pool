package server

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	_ "modernc.org/sqlite"
)

const stateVersion = 1

type Storage struct {
	dataDir string
	dbPath  string

	mu sync.Mutex
	db *sql.DB
}

func defaultState() State {
	return State{
		Version: stateVersion,
		Settings: Settings{
			BindAddress:            "127.0.0.1",
			AllowLan:               false,
			LogLevel:               "info",
			BaseMixedPort:          30001,
			BaseControllerPort:     40001,
			MaxLogLines:            800,
			HealthCheckIntervalSec: 60,
			SubscriptionRefreshMin: 0,
			HealthCheckURL:         "http://www.gstatic.com/generate_204",
			ExportHost:             "",
			ProxyAuth:              generateProxyAuth(),
		},
		Subscriptions: []Subscription{},
		Instances:     []Instance{},
	}
}

func NewStorage(dataDir string) *Storage {
	return &Storage{
		dataDir: dataDir,
		dbPath:  filepath.Join(dataDir, "state.sqlite"),
	}
}

func (s *Storage) ensureDirs() error {
	if err := os.MkdirAll(s.dataDir, 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(s.dataDir, "instances"), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(s.dataDir, "subscriptions"), 0o755); err != nil {
		return err
	}
	return nil
}

func (s *Storage) openDBLocked() (*sql.DB, error) {
	if s.db != nil {
		return s.db, nil
	}
	if err := s.ensureDirs(); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", s.dbPath)
	if err != nil {
		return nil, err
	}
	stmts := []string{
		"PRAGMA journal_mode = WAL;",
		"PRAGMA foreign_keys = ON;",
		"PRAGMA busy_timeout = 5000;",
		"CREATE TABLE IF NOT EXISTS kv (key TEXT PRIMARY KEY, value TEXT NOT NULL);",
	}
	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			_ = db.Close()
			return nil, err
		}
	}
	s.db = db
	return db, nil
}

func (s *Storage) withDB(fn func(*sql.DB) error) error {
	s.mu.Lock()
	db, err := s.openDBLocked()
	s.mu.Unlock()
	if err != nil {
		return err
	}
	return fn(db)
}

func (s *Storage) GetKV(key string) (string, error) {
	var out string
	err := s.withDB(func(db *sql.DB) error {
		row := db.QueryRow("SELECT value FROM kv WHERE key = ?", key)
		if err := row.Scan(&out); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				out = ""
				return nil
			}
			return err
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	return out, nil
}

func (s *Storage) SetKV(key, value string) error {
	return s.withDB(func(db *sql.DB) error {
		_, err := db.Exec("INSERT OR REPLACE INTO kv (key, value) VALUES (?, ?)", key, value)
		return err
	})
}

func (s *Storage) DeleteKV(key string) error {
	return s.withDB(func(db *sql.DB) error {
		_, err := db.Exec("DELETE FROM kv WHERE key = ?", key)
		return err
	})
}

func (s *Storage) GetJSON(key string, out any) error {
	raw, err := s.GetKV(key)
	if err != nil {
		return err
	}
	if raw == "" {
		return sql.ErrNoRows
	}
	if err := json.Unmarshal([]byte(raw), out); err != nil {
		return err
	}
	return nil
}

func (s *Storage) SetJSON(key string, in any) error {
	b, err := json.Marshal(in)
	if err != nil {
		return err
	}
	return s.SetKV(key, string(b))
}

func normalizeState(st State) State {
	fallback := defaultState()
	if st.Version != stateVersion {
		return fallback
	}
	out := st
	if out.Settings.BindAddress == "" {
		out.Settings.BindAddress = fallback.Settings.BindAddress
	}
	switch out.Settings.LogLevel {
	case "silent", "error", "warning", "info", "debug":
	default:
		out.Settings.LogLevel = fallback.Settings.LogLevel
	}
	if out.Settings.BaseMixedPort <= 0 {
		out.Settings.BaseMixedPort = fallback.Settings.BaseMixedPort
	}
	if out.Settings.BaseControllerPort <= 0 {
		out.Settings.BaseControllerPort = fallback.Settings.BaseControllerPort
	}
	if out.Settings.MaxLogLines <= 0 {
		out.Settings.MaxLogLines = fallback.Settings.MaxLogLines
	}
	if out.Settings.HealthCheckIntervalSec < 0 {
		out.Settings.HealthCheckIntervalSec = fallback.Settings.HealthCheckIntervalSec
	}
	if out.Settings.SubscriptionRefreshMin < 0 {
		out.Settings.SubscriptionRefreshMin = fallback.Settings.SubscriptionRefreshMin
	}
	if out.Settings.HealthCheckURL == "" {
		out.Settings.HealthCheckURL = fallback.Settings.HealthCheckURL
	}
	if out.Settings.ProxyAuth.Username == "" || out.Settings.ProxyAuth.Password == "" {
		next := generateProxyAuth()
		next.Enabled = out.Settings.ProxyAuth.Enabled
		out.Settings.ProxyAuth = next
	}
	if out.Subscriptions == nil {
		out.Subscriptions = []Subscription{}
	}
	if out.Instances == nil {
		out.Instances = []Instance{}
	}
	for i := range out.Instances {
		_ = out.Instances[i].AutoSwitch
	}
	return out
}

func (s *Storage) LoadState() (State, error) {
	if err := s.ensureDirs(); err != nil {
		return State{}, err
	}
	var st State
	if err := s.GetJSON("state", &st); err == nil {
		return normalizeState(st), nil
	}

	legacyPath := filepath.Join(s.dataDir, "state.json")
	if b, err := os.ReadFile(legacyPath); err == nil {
		if err := json.Unmarshal(b, &st); err == nil {
			norm := normalizeState(st)
			if err := s.SetJSON("state", norm); err != nil {
				return State{}, err
			}
			_ = os.Rename(legacyPath, legacyPath+".bak")
			return norm, nil
		}
	}

	initial := defaultState()
	if err := s.SetJSON("state", initial); err != nil {
		return State{}, err
	}
	return initial, nil
}

func (s *Storage) SaveState(st State) error {
	return s.SetJSON("state", st)
}

func (s *Storage) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return nil
	}
	err := s.db.Close()
	s.db = nil
	return err
}

func (s *Storage) String() string {
	return fmt.Sprintf("Storage{%s}", s.dbPath)
}
