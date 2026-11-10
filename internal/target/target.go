// Package target は k6 の練習相手になる練習用サーバー（:8090）である．
//
// 遅延はすべて待機（time.Sleep）か同時実行数の制限で作り，CPU の計算量では作らない．
// どの PC でも同じコードなら同じ判定になるようにするためである．
package target

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// DemoUser と DemoPassword は POST /api/login で受け付ける唯一の組み合わせ．
	DemoUser     = "drill"
	DemoPassword = "k6-practice"

	itemsDelay  = 20 * time.Millisecond
	heavyDelay  = 200 * time.Millisecond
	slowMaxMS   = 5000
	maxUpload   = 5 << 20
	flakyPeriod = 10
)

type item struct {
	ID    int    `json:"id"`
	Name  string `json:"name"`
	Price int    `json:"price"`
}

var items = []item{
	{1, "ノート", 150}, {2, "えんぴつ", 80}, {3, "消しゴム", 100}, {4, "定規", 200},
	{5, "はさみ", 450}, {6, "のり", 120}, {7, "ホチキス", 600}, {8, "付箋", 300},
	{9, "クリップ", 90}, {10, "ファイル", 250},
}

// Server は練習用サーバーの状態（カウンタと順番待ち）を持つ．
type Server struct {
	flaky    atomic.Int64
	uploads  atomic.Int64
	tokenSeq atomic.Int64

	mu     sync.Mutex
	heavy  chan struct{} // 容量1のセマフォ．リセットのたびに作り直す
	tokens map[string]bool
}

// New は初期状態の練習用サーバーを返す．
func New() *Server {
	s := &Server{}
	s.Reset()
	return s
}

// Reset はカウンタと順番待ちを初期状態に戻す．実行器が各実行の前に呼ぶ．
// 前の実行で並んでいたリクエストは古いセマフォに残り，新しい実行を待たせない．
func (s *Server) Reset() {
	s.flaky.Store(0)
	s.uploads.Store(0)
	s.mu.Lock()
	s.heavy = make(chan struct{}, 1)
	s.tokens = map[string]bool{}
	s.mu.Unlock()
}

// Handler は練習用サーバーのルーティングを返す．
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/ping", s.ping)
	mux.HandleFunc("GET /api/items", s.listItems)
	mux.HandleFunc("GET /api/items/{id}", s.getItem)
	mux.HandleFunc("GET /api/slow", s.slow)
	mux.HandleFunc("GET /api/heavy", s.heavyHandler)
	mux.HandleFunc("GET /api/flaky", s.flakyHandler)
	mux.HandleFunc("POST /api/login", s.login)
	mux.HandleFunc("GET /api/me", s.me)
	mux.HandleFunc("POST /api/files", s.upload)
	mux.HandleFunc("POST /__reset", func(w http.ResponseWriter, r *http.Request) {
		s.Reset()
		writeJSON(w, http.StatusOK, map[string]any{"reset": true})
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "そのパスはありません")
	})
	return mux
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// sleep は ctx が切れたら早く戻る待機．
func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}

func (s *Server) ping(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"message": "pong"})
}

func (s *Server) listItems(w http.ResponseWriter, r *http.Request) {
	if !sleep(r.Context(), itemsDelay) {
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) getItem(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil || id < 1 || id > len(items) {
		writeError(w, http.StatusNotFound, "その id の品物はありません")
		return
	}
	writeJSON(w, http.StatusOK, items[id-1])
}

func (s *Server) slow(w http.ResponseWriter, r *http.Request) {
	ms, err := strconv.Atoi(r.URL.Query().Get("ms"))
	if err != nil || ms < 0 {
		writeError(w, http.StatusBadRequest, "ms には 0 以上の整数を指定してください")
		return
	}
	if ms > slowMaxMS {
		ms = slowMaxMS
	}
	if !sleep(r.Context(), time.Duration(ms)*time.Millisecond) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"waited_ms": ms})
}

// heavyHandler は同時に1件しか処理しない．同時に来たリクエストは順番待ちになる．
func (s *Server) heavyHandler(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	sem := s.heavy
	s.mu.Unlock()

	start := time.Now()
	select {
	case sem <- struct{}{}:
	case <-r.Context().Done():
		return
	}
	defer func() { <-sem }()
	queued := time.Since(start)
	if !sleep(r.Context(), heavyDelay) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"result":    "done",
		"queued_ms": queued.Milliseconds(),
	})
}

// flakyHandler は10件に1件の割合で 500 を返す．乱数ではなくカウンタで決める．
func (s *Server) flakyHandler(w http.ResponseWriter, r *http.Request) {
	n := s.flaky.Add(1)
	if n%flakyPeriod == 0 {
		writeError(w, http.StatusInternalServerError, "ときどき失敗します")
		return
	}
	writeJSON(w, http.StatusOK, map[string]int64{"count": n})
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		writeError(w, http.StatusUnsupportedMediaType, "Content-Type: application/json を付けて JSON を送ってください")
		return
	}
	var body struct {
		User     string `json:"user"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "本文を JSON として読めません")
		return
	}
	if body.User != DemoUser || body.Password != DemoPassword {
		writeError(w, http.StatusUnauthorized, "user か password が違います")
		return
	}
	token := "drill-" + strconv.FormatInt(s.tokenSeq.Add(1), 10)
	s.mu.Lock()
	s.tokens[token] = true
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]string{"token": token})
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	s.mu.Lock()
	valid := ok && s.tokens[token]
	s.mu.Unlock()
	if !valid {
		writeError(w, http.StatusUnauthorized, "Authorization: Bearer <token> が必要です")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"user": DemoUser})
}

func (s *Server) upload(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("X-User-ID") == "" {
		writeError(w, http.StatusUnauthorized, "ヘッダー X-User-ID が必要です")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxUpload)
	f, _, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "multipart の file 欄がありません")
		return
	}
	defer f.Close()
	size, err := io.Copy(io.Discard, f)
	if err != nil {
		writeError(w, http.StatusBadRequest, "ファイルを読めません")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]int64{"id": s.uploads.Add(1), "size": size})
}
