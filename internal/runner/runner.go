// Package runner は学習者のコードを一時ディレクトリに書き出し，k6 inspect と k6 run を実行して出力を回収する．
package runner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// 実行の上限．
const (
	MaxVUs      = 100
	MaxDuration = 60 * time.Second
	RunTimeout  = 120 * time.Second
	outputLimit = 512 << 10
)

// Runner は k6 を起動する．同時に1つしか実行しない（練習用サーバーの状態を共有するため）．
type Runner struct {
	K6      string // k6 の実行ファイル
	BaseURL string // -e BASE_URL に渡す練習用サーバーの URL
	Assets  fs.FS  // assets/ を含むファイルシステム
	Reset   func() // 実行の前に練習用サーバーを初期状態に戻す
	Timeout time.Duration

	mu sync.Mutex
}

// ErrBusy はほかの実行が終わっていないことを表す．
var ErrBusy = errors.New("ほかのテストを実行中です．終わるまで待ってください")

// ThresholdResult は閾値の式1つ分の結果．
type ThresholdResult struct {
	Name        string         `json:"name"`
	Expr        string         `json:"expr"`
	OK          bool           `json:"ok"`
	Actual      string         `json:"actual"`
	Value       float64        `json:"-"`
	Count       int            `json:"count"`
	Tagged      bool           `json:"tagged"`
	FromSummary bool           `json:"from_summary"`
	URLs        map[string]int `json:"urls,omitempty"`
}

// Result は1回の実行の結果．
type Result struct {
	Phase        string            `json:"phase"` // inspect, limit, run
	Error        string            `json:"error,omitempty"`
	ExitCode     int               `json:"exit_code"`
	TimedOut     bool              `json:"timed_out"`
	Stdout       string            `json:"stdout"`
	Stderr       string            `json:"stderr"`
	Inspect      *Inspect          `json:"inspect,omitempty"`
	Thresholds   []ThresholdResult `json:"thresholds"`
	HTTPReqs     int               `json:"http_reqs"`
	ChecksTotal  int               `json:"checks_total"`
	ChecksPassed int               `json:"checks_passed"`
	CheckNames   []string          `json:"check_names"`
	Requests     []RequestGroup    `json:"-"`
	Seconds      float64           `json:"seconds"`
}

// Log は k6 の標準出力と標準エラーを合わせたもの．
func (r *Result) Log() string { return r.Stdout + "\n" + r.Stderr }

// workdir はスクリプトと assets を一時ディレクトリに書き出す．
// open() の相対パスはスクリプトの場所を基準に解決されるため，同じ場所に置く．
func (r *Runner) workdir(code string, assets []string) (string, error) {
	dir, err := os.MkdirTemp("", "k6-drill-")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dir, "script.js"), []byte(code), 0o600); err != nil {
		os.RemoveAll(dir)
		return "", err
	}
	for _, a := range assets {
		b, err := fs.ReadFile(r.Assets, path.Join("assets", a))
		if err != nil {
			os.RemoveAll(dir)
			return "", err
		}
		if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(a)), b, 0o600); err != nil {
			os.RemoveAll(dir)
			return "", err
		}
	}
	return dir, nil
}

// env は学習者の環境にある K6_ の変数を除き，集計表示を色なしにする．
func env() []string {
	var out []string
	for _, e := range os.Environ() {
		if !strings.HasPrefix(e, "K6_") {
			out = append(out, e)
		}
	}
	return append(out, "K6_NO_USAGE_REPORT=true", "K6_NO_COLOR=true", "NO_COLOR=1")
}

type cmdOut struct {
	stdout, stderr string
	exit           int
	timedOut       bool
	err            error
}

func (r *Runner) k6(ctx context.Context, dir string, timeout time.Duration, args ...string) cmdOut {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, r.K6, args...)
	cmd.Dir = dir
	cmd.Env = env()
	cmd.WaitDelay = 5 * time.Second
	var so, se limitedBuffer
	cmd.Stdout, cmd.Stderr = &so, &se
	err := cmd.Run()
	o := cmdOut{stdout: so.String(), stderr: se.String()}
	var ee *exec.ExitError
	switch {
	case ctx.Err() == context.DeadlineExceeded:
		o.timedOut, o.exit = true, -1
	case errors.As(err, &ee):
		o.exit = ee.ExitCode()
	case err != nil:
		o.err, o.exit = err, -1
	}
	return o
}

// Inspect はコードを k6 inspect にかけて options を解決する．
func (r *Runner) Inspect(ctx context.Context, code string, assets []string) (*Inspect, cmdOut, error) {
	dir, err := r.workdir(code, assets)
	if err != nil {
		return nil, cmdOut{}, err
	}
	defer os.RemoveAll(dir)
	return r.inspect(ctx, dir)
}

func (r *Runner) inspect(ctx context.Context, dir string) (*Inspect, cmdOut, error) {
	args := []string{"inspect", "--execution-requirements", "-e", "BASE_URL=" + r.BaseURL, "script.js"}
	o := r.k6(ctx, dir, 30*time.Second, args...)
	if o.exit != 0 && strings.Contains(o.stderr+o.stdout, "unknown flag") {
		args = append(args[:1], args[2:]...)
		o = r.k6(ctx, dir, 30*time.Second, args...)
	}
	if o.err != nil {
		return nil, o, fmt.Errorf("k6 を起動できません: %w", o.err)
	}
	if o.exit != 0 {
		return nil, o, errors.New("k6 inspect が失敗しました")
	}
	in, err := ParseInspect(extractJSON(o.stdout))
	return in, o, err
}

// extractJSON は k6 inspect の出力から JSON の部分だけを取り出す（前にバナーなどが出る場合に備える）．
func extractJSON(s string) []byte {
	if i := strings.Index(s, "{"); i >= 0 {
		if j := strings.LastIndex(s, "}"); j > i {
			return []byte(s[i : j+1])
		}
	}
	return []byte(s)
}

// Run は事前確認，初期化，実行，集計を行う．
func (r *Runner) Run(ctx context.Context, code string, assets []string) (*Result, error) {
	if !r.mu.TryLock() {
		return nil, ErrBusy
	}
	defer r.mu.Unlock()

	dir, err := r.workdir(code, assets)
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)

	res := &Result{Phase: "inspect"}
	in, o, err := r.inspect(ctx, dir)
	res.Stdout, res.Stderr, res.ExitCode = o.stdout, o.stderr, o.exit
	if err != nil {
		res.Error = err.Error()
		return res, nil
	}
	res.Inspect = in

	res.Phase = "limit"
	if in.MaxVUs > MaxVUs {
		res.Error = fmt.Sprintf("VU が %d あります．このアプリでは %d 以下にしてください", in.MaxVUs, MaxVUs)
		return res, nil
	}
	if in.Duration > MaxDuration {
		res.Error = fmt.Sprintf("実行時間が %v あります．このアプリでは %v 以下にしてください", in.Duration, MaxDuration)
		return res, nil
	}

	res.Phase = "run"
	if r.Reset != nil {
		r.Reset()
	}
	timeout := r.Timeout
	if timeout == 0 {
		timeout = RunTimeout
	}
	start := time.Now()
	o = r.k6(ctx, dir, timeout, "run", "-e", "BASE_URL="+r.BaseURL, "--out", "json=points.json", writeEntry(dir, code))
	res.Seconds = time.Since(start).Seconds()
	res.Stdout, res.Stderr, res.ExitCode, res.TimedOut = o.stdout, o.stderr, o.exit, o.timedOut
	if o.err != nil {
		res.Error = "k6 を起動できません: " + o.err.Error()
		return res, nil
	}
	if o.timedOut {
		res.Error = fmt.Sprintf("%v を超えたので k6 を止めました", timeout)
	}

	names := make([]string, 0, len(in.Thresholds))
	for n := range in.Thresholds {
		names = append(names, n)
	}
	sort.Strings(names)
	var pts *Points
	if f, err := os.Open(filepath.Join(dir, "points.json")); err == nil {
		pts, _ = ReadPoints(f, names)
		f.Close()
	}
	if pts == nil {
		pts, _ = ReadPoints(strings.NewReader(""), names)
	}
	res.HTTPReqs, res.ChecksTotal, res.ChecksPassed = pts.HTTPReqs, pts.ChecksTotal, pts.ChecksPassed
	res.Requests = pts.Requests
	for n := range pts.CheckNames {
		res.CheckNames = append(res.CheckNames, n)
	}
	sort.Strings(res.CheckNames)
	res.Thresholds = buildThresholds(names, in.Thresholds, pts, ParseSummaryThresholds(o.stdout))
	return res, nil
}

// buildThresholds は閾値ごとの結果をまとめる．合否と実測値は k6 の集計表示を優先し，
// 読めなかったものは計測値から k6 と同じ方法で計算する．測定件数は計測値から数える．
func buildThresholds(names []string, exprs map[string][]string, pts *Points, summary []SummaryThreshold) []ThresholdResult {
	fromSummary := map[string]SummaryThreshold{}
	for _, s := range summary {
		fromSummary[ParseMetricName(s.Metric).Key()+"\x00"+NormalizeExpr(s.Expr)] = s
	}
	var out []ThresholdResult
	for _, name := range names {
		mn := ParseMetricName(name)
		st := pts.thresholdStats[mn.Key()]
		info := pts.MetricTypes[mn.Base]
		for _, ex := range exprs[name] {
			tr := ThresholdResult{Name: name, Expr: ex, Tagged: len(mn.Tags) > 0}
			if st != nil {
				tr.Count, tr.URLs = len(st.values), st.urls
			}
			if e, err := ParseExpr(ex); err == nil {
				var vals []float64
				if st != nil {
					vals = st.values
				}
				tr.Value = Aggregate(e, info.Type, vals, pts.Seconds)
				tr.OK = e.Holds(tr.Value)
				tr.Actual = FormatValue(e, info.Type, info.Contains, tr.Value)
			}
			if s, ok := fromSummary[mn.Key()+"\x00"+NormalizeExpr(ex)]; ok {
				tr.OK, tr.FromSummary = s.OK, true
				if s.Actual != "" {
					tr.Actual = s.Actual
				}
			}
			out = append(out, tr)
		}
	}
	return out
}

// Version は `k6 version` の出力を返す．
func (r *Runner) Version(ctx context.Context) (string, error) {
	o := r.k6(ctx, "", 15*time.Second, "version")
	if o.err != nil {
		return "", o.err
	}
	if o.exit != 0 {
		return "", fmt.Errorf("k6 version が終了コード %d で終わりました: %s", o.exit, strings.TrimSpace(o.stderr))
	}
	return strings.TrimSpace(o.stdout), nil
}

var versionRe = regexp.MustCompile(`v(\d+)\.(\d+)\.(\d+)`)

// MajorVersion は `k6 v1.2.3 (...)` から 1 を取り出す．読めなければ -1．
func MajorVersion(s string) int {
	m := versionRe.FindStringSubmatch(s)
	if m == nil {
		return -1
	}
	n := 0
	fmt.Sscan(m[1], &n)
	return n
}

// limitedBuffer は大量のログで記憶を使い切らないよう，先頭と末尾だけを残す．
type limitedBuffer struct {
	head    bytes.Buffer
	tail    []byte
	dropped int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	if room := outputLimit - b.head.Len(); room > 0 {
		k := min(room, len(p))
		b.head.Write(p[:k])
		p = p[k:]
	}
	if len(p) > 0 {
		b.tail = append(b.tail, p...)
		if over := len(b.tail) - outputLimit/4; over > 0 {
			b.dropped += over
			b.tail = b.tail[over:]
		}
	}
	return n, nil
}

func (b *limitedBuffer) String() string {
	if b.dropped == 0 {
		return b.head.String() + string(b.tail)
	}
	return fmt.Sprintf("%s\n… （出力が多いため %d バイトを省略しました） …\n%s", b.head.String(), b.dropped, b.tail)
}
