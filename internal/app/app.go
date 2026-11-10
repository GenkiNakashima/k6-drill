// Package app は画面の配信と，画面が呼ぶ API（問題，実行，答え合わせ，進捗）をまとめる．
package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"runtime"
	"strings"
	"time"

	"k6-drill/internal/analyze"
	"k6-drill/internal/exercise"
	"k6-drill/internal/judge"
	"k6-drill/internal/progress"
	"k6-drill/internal/runner"
)

// App は画面の API．
type App struct {
	Set       *exercise.Set
	Runner    *runner.Runner
	Store     *progress.Store
	Web       fs.FS
	TargetURL string
	Unlock    bool // true なら基本問題を終えなくても応用問題を開ける（開発用）
}

// Handler は画面と API のルーティングを返す．
func (a *App) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /", http.FileServerFS(a.Web))
	mux.HandleFunc("GET /api/status", a.status)
	mux.HandleFunc("GET /api/exercises", a.list)
	mux.HandleFunc("GET /api/exercises/{id}", a.get)
	mux.HandleFunc("PUT /api/exercises/{id}/code", a.saveCode)
	mux.HandleFunc("POST /api/exercises/{id}/check", a.check)
	mux.HandleFunc("POST /api/exercises/{id}/run", a.run)
	mux.HandleFunc("POST /api/exercises/{id}/reveal", a.reveal)
	mux.HandleFunc("GET /api/recipes", a.recipes)
	mux.HandleFunc("POST /api/exercises/{id}/quiz", a.quiz)
	mux.HandleFunc("GET /api/settings", a.getSettings)
	mux.HandleFunc("PUT /api/settings", a.putSettings)
	mux.HandleFunc("POST /api/progress/reset", a.resetProgress)
	return guard(mux)
}

// guard はほかのサイトからこの API を呼ばれないようにする．
// 実行 API は手元の PC で k6 を動かすので，Host を確かめ（DNS rebinding 対策），
// 書き換える要求には独自ヘッダーを求める（ほかのサイトからは事前確認なしに送れない）．
func guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.Host)
		if err != nil {
			host = r.Host
		}
		if host != "127.0.0.1" && host != "localhost" {
			http.Error(w, "forbidden host", http.StatusForbidden)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Header.Get("X-K6-Drill") != "1" {
			http.Error(w, "missing X-K6-Drill header", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func readJSON(r *http.Request, v any) error {
	return json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20)).Decode(v)
}

// InstallGuide は OS ごとの k6 の入れ方．
var InstallGuide = map[string][]string{
	"darwin": {"brew install k6"},
	"windows": {
		"winget install k6 --source winget",
		"（または）choco install k6",
	},
	"linux": {
		"sudo gpg -k",
		"sudo gpg --no-default-keyring --keyring /usr/share/keyrings/k6-archive-keyring.gpg --keyserver hkp://keyserver.ubuntu.com:80 --recv-keys C5AD17C747E3415A3642D57D77C6C491D6AC1D69",
		`echo "deb [signed-by=/usr/share/keyrings/k6-archive-keyring.gpg] https://dl.k6.io/deb stable main" | sudo tee /etc/apt/sources.list.d/k6.list`,
		"sudo apt-get update",
		"sudo apt-get install k6",
	},
}

func (a *App) status(w http.ResponseWriter, r *http.Request) {
	out := map[string]any{
		"os":          runtime.GOOS,
		"arch":        runtime.GOARCH,
		"install":     InstallGuide,
		"install_doc": "https://grafana.com/docs/k6/latest/set-up/install-k6/",
		"k6_path":     a.Runner.K6,
	}
	ver, err := a.Runner.Version(r.Context())
	switch {
	case err != nil:
		msg := err.Error()
		if strings.Contains(msg, "bad CPU type") || strings.Contains(msg, "exec format error") {
			msg += "（この PC の CPU に合わない k6 が入っています．" + runtime.GOARCH + " 向けの k6 を入れ直してください）"
		}
		out["k6_ok"], out["k6_error"] = false, msg
	case runner.MajorVersion(ver) < 1:
		out["k6_ok"], out["k6_version"], out["k6_error"] = false, ver, "k6 v1 以上が必要です．新しい版を入れてください"
	default:
		out["k6_ok"], out["k6_version"] = true, ver
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", a.TargetURL+"/api/ping", nil)
	res, err := http.DefaultClient.Do(req)
	out["target_ok"] = err == nil && res.StatusCode == 200
	out["target_url"] = a.TargetURL
	if err == nil {
		res.Body.Close()
	}
	writeJSON(w, http.StatusOK, out)
}

// passed は進捗から，その問題を合格したかを決める．
func passed(e *exercise.Exercise, p progress.Entry) bool {
	switch {
	case e.Type == "quiz":
		return p.QuizPassed
	case len(e.Quiz) > 0:
		return p.QuizPassed && p.CodePassed
	}
	return p.CodePassed
}

func (a *App) locked(e *exercise.Exercise) bool {
	if a.Unlock || e.Level != "advanced" {
		return false
	}
	for _, b := range a.Set.List {
		if b.Level == "basic" && !b.Optional && a.Store.Get(b.ID).Status != progress.Passed {
			return true
		}
	}
	return false
}

type summary struct {
	ID       string `json:"id"`
	Level    string `json:"level"`
	Title    string `json:"title"`
	Minutes  int    `json:"minutes"`
	Optional bool   `json:"optional"`
	Type     string `json:"type"`
	Status   string `json:"status"`
	Locked   bool   `json:"locked"`
}

func (a *App) list(w http.ResponseWriter, r *http.Request) {
	var out []summary
	done, total, remain := 0, 0, 0
	for _, e := range a.Set.List {
		st := a.Store.Get(e.ID).Status
		out = append(out, summary{e.ID, e.Level, e.Title, e.Minutes, e.Optional, e.Type, st, a.locked(e)})
		if e.Optional {
			continue
		}
		total++
		if st == progress.Passed {
			done++
		} else {
			remain += e.Minutes
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"exercises": out, "done": done, "total": total, "remaining_minutes": remain,
	})
}

type publicChoice struct {
	Text string `json:"text"`
}

type publicQuestion struct {
	Question string         `json:"question"`
	Code     string         `json:"code,omitempty"`
	Choices  []publicChoice `json:"choices"`
}

func (a *App) exercise(w http.ResponseWriter, r *http.Request) *exercise.Exercise {
	e := a.Set.Get(r.PathValue("id"))
	if e == nil {
		writeErr(w, http.StatusNotFound, "その問題はありません")
		return nil
	}
	if a.locked(e) {
		writeErr(w, http.StatusForbidden, "応用問題は，基本問題をすべて合格すると開きます")
		return nil
	}
	return e
}

func (a *App) detail(e *exercise.Exercise) map[string]any {
	p := a.Store.Get(e.ID)
	qs := make([]publicQuestion, len(e.Quiz))
	for i, q := range e.Quiz {
		qs[i] = publicQuestion{Question: q.Question, Code: q.Code}
		for _, c := range q.Choices {
			qs[i].Choices = append(qs[i].Choices, publicChoice{c.Text})
		}
	}
	out := map[string]any{"exercise": e, "quiz": qs, "progress": p, "can_reveal": a.canReveal(e, p)}
	if p.Status == progress.Passed {
		out["explanation"] = e.Explanation
	}
	if e.HasCode() && (p.CodePassed || p.Revealed) {
		out["solution"] = e.Solution
	}
	return out
}

// revealAfter は，この回数だけ採点で不合格になると模範コードを見られるようにする回数．
const revealAfter = 2

func (a *App) canReveal(e *exercise.Exercise, p progress.Entry) bool {
	return e.HasCode() && !p.CodePassed && !p.Revealed && p.Fails >= revealAfter
}

func (a *App) get(w http.ResponseWriter, r *http.Request) {
	e := a.exercise(w, r)
	if e == nil {
		return
	}
	p, _ := a.Store.Update(e.ID, func(p *progress.Entry) {
		if p.OpenedAt == nil {
			now := time.Now()
			p.OpenedAt = &now
			if p.Status == progress.NotStarted {
				p.Status = progress.Trying
			}
		}
	})
	if p.Attempts == 0 {
		_ = a.Store.Record("open", map[string]any{"id": e.ID})
	}
	writeJSON(w, http.StatusOK, a.detail(e))
}

func (a *App) saveCode(w http.ResponseWriter, r *http.Request) {
	e := a.exercise(w, r)
	if e == nil {
		return
	}
	var body struct{ Code *string }
	if err := readJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, "要求を読めません")
		return
	}
	_, err := a.Store.Update(e.ID, func(p *progress.Entry) {
		if body.Code == nil || *body.Code == e.Starter {
			p.Code = ""
		} else {
			p.Code = *body.Code
		}
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"saved": true})
}

// Check はコードを静的解析で採点する．画面の API と自動テストの両方から使う．
func (a *App) Check(e *exercise.Exercise, code string) judge.StaticVerdict {
	in := judge.StaticInput{Exercise: e, Code: code}
	if e.Judge.Static.ThresholdsUnchanged {
		in.Starter, _ = analyze.Parse(e.Starter)
	}
	return judge.EvaluateStatic(in)
}

// RunK6 はコードを k6 で実行する（拡張機能）．
func (a *App) RunK6(ctx context.Context, e *exercise.Exercise, code string) (*judge.Verdict, error) {
	res, err := a.Runner.Run(ctx, code, e.Assets)
	if err != nil {
		return nil, err
	}
	v := judge.EvaluateRun(judge.Input{Exercise: e, Result: res})
	return &v, nil
}

func (a *App) codeBody(w http.ResponseWriter, r *http.Request) (*exercise.Exercise, string, bool) {
	e := a.exercise(w, r)
	if e == nil {
		return nil, "", false
	}
	if !e.HasCode() {
		writeErr(w, http.StatusBadRequest, "この問題は選択式です")
		return nil, "", false
	}
	var body struct{ Code string }
	if err := readJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, "要求を読めません")
		return nil, "", false
	}
	return e, body.Code, true
}

func failureCodes(fs []judge.Failure) []string {
	codes := make([]string, len(fs))
	for i, f := range fs {
		codes[i] = f.Code
	}
	return codes
}

func (a *App) check(w http.ResponseWriter, r *http.Request) {
	e, code, ok := a.codeBody(w, r)
	if !ok {
		return
	}
	v := a.Check(e, code)
	p, _ := a.Store.Update(e.ID, func(p *progress.Entry) {
		if code != e.Starter {
			p.Code = code
		}
		p.Attempts++
		if p.Status == progress.NotStarted {
			p.Status = progress.Trying
		}
		if v.Passed {
			p.CodePassed = true
		} else {
			p.Fails++
		}
		a.markPassed(e, p)
	})
	_ = a.Store.Record("check", map[string]any{"id": e.ID, "passed": v.Passed, "failures": failureCodes(v.Failures), "attempt": p.Attempts})
	out := a.detail(e)
	out["verdict"] = v
	writeJSON(w, http.StatusOK, out)
}

func (a *App) run(w http.ResponseWriter, r *http.Request) {
	e, code, ok := a.codeBody(w, r)
	if !ok {
		return
	}
	v, err := a.RunK6(r.Context(), e, code)
	if errors.Is(err, runner.ErrBusy) {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	p, _ := a.Store.Update(e.ID, func(p *progress.Entry) { p.Runs++ })
	_ = a.Store.Record("run", map[string]any{"id": e.ID, "passed": v.Passed, "exit_code": v.ExitCode, "failures": failureCodes(v.Failures), "run": p.Runs})
	writeJSON(w, http.StatusOK, map[string]any{"run": v})
}

func (a *App) reveal(w http.ResponseWriter, r *http.Request) {
	e := a.exercise(w, r)
	if e == nil {
		return
	}
	if !a.canReveal(e, a.Store.Get(e.ID)) && !a.Store.Get(e.ID).CodePassed {
		writeErr(w, http.StatusForbidden, fmt.Sprintf("模範コードは，採点で %d 回不合格になると見られます", revealAfter))
		return
	}
	_, _ = a.Store.Update(e.ID, func(p *progress.Entry) {
		if !p.CodePassed {
			p.Revealed = true
		}
	})
	_ = a.Store.Record("reveal", map[string]any{"id": e.ID})
	writeJSON(w, http.StatusOK, a.detail(e))
}

type recipe struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Scenario string `json:"scenario"`
	Open     bool   `json:"open"`
	Code     string `json:"code,omitempty"`
}

// recipes は「こういう状況ではこう書く」の一覧．合格した問題（または模範コードを見た問題）のコードだけを見せる．
func (a *App) recipes(w http.ResponseWriter, r *http.Request) {
	var out []recipe
	for _, e := range a.Set.List {
		if !e.HasCode() || e.Scenario == "" {
			continue
		}
		p := a.Store.Get(e.ID)
		rc := recipe{ID: e.ID, Title: e.Title, Scenario: e.Scenario, Open: p.CodePassed || p.Revealed}
		if rc.Open {
			rc.Code = e.Solution
		}
		out = append(out, rc)
	}
	writeJSON(w, http.StatusOK, map[string]any{"recipes": out})
}

func (a *App) markPassed(e *exercise.Exercise, p *progress.Entry) {
	if p.Status != progress.Passed && passed(e, *p) {
		now := time.Now()
		p.Status, p.PassedAt = progress.Passed, &now
	}
}

type quizResult struct {
	Correct      bool     `json:"correct"`
	CorrectIndex int      `json:"correct_index"`
	Explains     []string `json:"explains"`
}

func (a *App) quiz(w http.ResponseWriter, r *http.Request) {
	e := a.exercise(w, r)
	if e == nil {
		return
	}
	var body struct{ Answers []int }
	if err := readJSON(r, &body); err != nil || len(body.Answers) != len(e.Quiz) {
		writeErr(w, http.StatusBadRequest, "すべての設問に答えてください")
		return
	}
	all := true
	results := make([]quizResult, len(e.Quiz))
	for i, q := range e.Quiz {
		qr := quizResult{CorrectIndex: -1}
		for j, c := range q.Choices {
			qr.Explains = append(qr.Explains, c.Explain)
			if c.Correct {
				qr.CorrectIndex = j
			}
		}
		qr.Correct = body.Answers[i] == qr.CorrectIndex
		all = all && qr.Correct
		results[i] = qr
	}
	_, _ = a.Store.Update(e.ID, func(p *progress.Entry) {
		p.Attempts++
		if all {
			p.QuizPassed = true
		}
		a.markPassed(e, p)
	})
	_ = a.Store.Record("quiz", map[string]any{"id": e.ID, "passed": all, "answers": body.Answers})
	out := a.detail(e)
	out["quiz_results"] = results
	out["quiz_all_correct"] = all
	writeJSON(w, http.StatusOK, out)
}

func (a *App) getSettings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"settings": a.Store.Settings(), "dir": a.Store.Dir()})
}

func (a *App) putSettings(w http.ResponseWriter, r *http.Request) {
	var st progress.Settings
	if err := readJSON(r, &st); err != nil {
		writeErr(w, http.StatusBadRequest, "要求を読めません")
		return
	}
	if err := a.Store.SetSettings(st); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"settings": st})
}

func (a *App) resetProgress(w http.ResponseWriter, r *http.Request) {
	if err := a.Store.Reset(); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"reset": true})
}
