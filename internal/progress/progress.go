// Package progress は合否と書きかけのコードを ~/.k6-drill/progress.json に保存する．
package progress

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// 問題ごとの状態．
const (
	NotStarted = "not_started"
	Trying     = "trying"
	Passed     = "passed"
)

// Entry は問題1つの進捗．
type Entry struct {
	Status     string     `json:"status"`
	Code       string     `json:"code,omitempty"`
	QuizPassed bool       `json:"quiz_passed,omitempty"`
	CodePassed bool       `json:"code_passed,omitempty"`
	Attempts   int        `json:"attempts"`
	Fails      int        `json:"fails,omitempty"`    // 採点で不合格だった回数
	Revealed   bool       `json:"revealed,omitempty"` // 合格する前に模範コードを見た
	Runs       int        `json:"runs,omitempty"`     // k6 で実機実行した回数（拡張機能）
	OpenedAt   *time.Time `json:"opened_at,omitempty"`
	PassedAt   *time.Time `json:"passed_at,omitempty"`
}

// Settings は設定画面で変えられるもの．
type Settings struct {
	// Record が true のとき，問題ごとの操作を record.jsonl に追記する（初期値は無効）．
	Record bool `json:"record"`
}

type data struct {
	Version   int               `json:"version"`
	Exercises map[string]*Entry `json:"exercises"`
	Settings  Settings          `json:"settings"`
}

// Store は進捗をファイルに保存する．
type Store struct {
	dir string
	mu  sync.Mutex
	d   data
}

// DefaultDir はホームディレクトリの .k6-drill．
func DefaultDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".k6-drill"), nil
}

// Open は dir/progress.json を読む．なければ空の進捗から始める．
func Open(dir string) (*Store, error) {
	s := &Store{dir: dir, d: data{Version: 1, Exercises: map[string]*Entry{}}}
	b, err := os.ReadFile(s.path())
	switch {
	case errors.Is(err, os.ErrNotExist):
		return s, nil
	case err != nil:
		return nil, err
	}
	if err := json.Unmarshal(b, &s.d); err != nil {
		// 壊れたファイルは残しておき，空から始める
		_ = os.Rename(s.path(), s.path()+".broken")
		s.d = data{Version: 1, Exercises: map[string]*Entry{}}
	}
	if s.d.Exercises == nil {
		s.d.Exercises = map[string]*Entry{}
	}
	return s, nil
}

// Dir は保存先のディレクトリ．
func (s *Store) Dir() string { return s.dir }

func (s *Store) path() string { return filepath.Join(s.dir, "progress.json") }

func (s *Store) saveLocked() error {
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s.d, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path() + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path())
}

// Get は問題の進捗の写しを返す．
func (s *Store) Get(id string) Entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := s.d.Exercises[id]; e != nil {
		return *e
	}
	return Entry{Status: NotStarted}
}

// Update は問題の進捗を書き換えて保存する．
func (s *Store) Update(id string, f func(e *Entry)) (Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e := s.d.Exercises[id]
	if e == nil {
		e = &Entry{Status: NotStarted}
		s.d.Exercises[id] = e
	}
	f(e)
	return *e, s.saveLocked()
}

// Settings は設定を返す．
func (s *Store) Settings() Settings {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.d.Settings
}

// SetSettings は設定を保存する．
func (s *Store) SetSettings(st Settings) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.d.Settings = st
	return s.saveLocked()
}

// Reset は合否と書きかけのコードをすべて消す．設定と記録はそのまま残す．
func (s *Store) Reset() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.d.Exercises = map[string]*Entry{}
	return s.saveLocked()
}

// Record は設定で記録が有効なときだけ，操作を record.jsonl に1行追記する．
func (s *Store) Record(event string, fields map[string]any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.d.Settings.Record {
		return nil
	}
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return err
	}
	rec := map[string]any{"time": time.Now().Format(time.RFC3339), "event": event}
	for k, v := range fields {
		rec[k] = v
	}
	b, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(s.dir, "record.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(b, '\n'))
	return err
}
