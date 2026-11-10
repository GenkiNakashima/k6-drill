// Package judge は問題ファイルの judge の条件を確かめ，満たさなかった条件に合うヒントを選ぶ．
//
// 採点は静的解析（static.go の EvaluateStatic）で行う．k6 での実機実行（このファイルの EvaluateRun）は
// 拡張機能で，学習者の PC に k6 があるときだけ使い，結果は合否に数えない．
package judge

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"k6-drill/internal/exercise"
	"k6-drill/internal/runner"
)

// Failure は満たさなかった条件1つ．
type Failure struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Verdict は実機実行の判定の結果．
type Verdict struct {
	Passed      bool           `json:"passed"`
	Failures    []Failure      `json:"failures"`
	Hints       []string       `json:"hints"`
	ExitCode    int            `json:"exit_code"`
	ExitMeaning string         `json:"exit_meaning"`
	ExitNote    string         `json:"exit_note"`
	ZeroSample  bool           `json:"zero_sample"`
	Result      *runner.Result `json:"result"`
}

// Input は実機実行の判定に必要なもの．
type Input struct {
	Exercise *exercise.Exercise
	Result   *runner.Result
}

// ExitMeanings は終了コードの意味と画面に出す説明．
var ExitMeanings = map[int][2]string{
	0:   {"閾値がすべて合格，または閾値がない", "0 は「何かを検証できた」ことを意味しない"},
	99:  {"閾値のどれかが不合格", "劣化を検出できた．応用問題の多くはこれが正解"},
	104: {"options の書き方が誤っている", "閾値の値が配列でない，などを疑う"},
	107: {"スクリプトの例外（init 段階のエラーなど）", "構文や open() のファイル名を疑う"},
	-1:  {"k6 が終了しなかった，または起動できなかった", "時間がかかりすぎていないか，k6 が入っているかを確かめる"},
}

type checker struct {
	in       Input
	failures []Failure
}

func (c *checker) fail(code, format string, args ...any) {
	c.failures = append(c.failures, Failure{Code: code, Message: fmt.Sprintf(format, args...)})
}

func (c *checker) failed(code string) bool {
	for _, f := range c.failures {
		if f.Code == code {
			return true
		}
	}
	return false
}

// EvaluateRun は k6 の実行結果を judge.run の条件と照らし合わせる．judge.run がなければ，k6 が最後まで動いたかだけを見る．
func EvaluateRun(in Input) Verdict {
	c := &checker{in: in}
	res := in.Result

	ran := res != nil && res.Phase == "run" && res.Error == ""
	if res != nil && !ran {
		msg := res.Error
		if msg == "" {
			msg = "k6 を実行できませんでした"
		}
		c.fail("run.not_executed", "%s", msg)
	}
	if ran && in.Exercise.Judge.Run != nil {
		c.run(*in.Exercise.Judge.Run, res)
	}

	v := Verdict{Passed: len(c.failures) == 0, Failures: c.failures, Result: res}
	if res != nil {
		v.ExitCode = res.ExitCode
		m, ok := ExitMeanings[res.ExitCode]
		if !ok {
			m = [2]string{"そのほかのエラー", "k6 の出力全文を確かめる"}
		}
		v.ExitMeaning, v.ExitNote = m[0], m[1]
		for _, t := range res.Thresholds {
			if t.Count == 0 {
				v.ZeroSample = true
			}
		}
	}
	if !v.Passed {
		v.Hints = c.hints(v)
	}
	if v.Failures == nil {
		v.Failures = []Failure{}
	}
	return v
}

// unescape は正規表現を画面に出すために，`\.` などの逃がし文字を外す．
func unescape(p string) string {
	return regexp.MustCompile(`\\(.)`).ReplaceAllString(p, "$1")
}

func thresholdSig(th map[string][]string) string {
	var parts []string
	for name, exprs := range th {
		norm := make([]string, len(exprs))
		for i, e := range exprs {
			norm[i] = runner.NormalizeExpr(e)
		}
		sort.Strings(norm)
		parts = append(parts, runner.ParseMetricName(name).Key()+"="+strings.Join(norm, "|"))
	}
	sort.Strings(parts)
	return strings.Join(parts, ";")
}

func (c *checker) run(j exercise.RunJudge, res *runner.Result) {
	if j.ExitCode != nil && res.ExitCode != *j.ExitCode {
		c.fail("run.exit_code", "終了コードが %d です．%d になるようにしてください", res.ExitCode, *j.ExitCode)
	}
	if j.MinHTTPReqs != nil && res.HTTPReqs < *j.MinHTTPReqs {
		c.fail("run.min_http_reqs", "送ったリクエストが %d 件です（%d 件以上が必要）", res.HTTPReqs, *j.MinHTTPReqs)
	}
	if j.MinChecks > 0 && res.ChecksTotal < j.MinChecks {
		c.fail("run.min_checks", "check の実行が %d 回です（%d 回以上が必要）", res.ChecksTotal, j.MinChecks)
	}
	if j.MinCheckNames > 0 && len(res.CheckNames) < j.MinCheckNames {
		c.fail("run.min_check_names", "check の項目が %d 種類です（%d 種類以上が必要）", len(res.CheckNames), j.MinCheckNames)
	}
	if j.ChecksRateMin != nil {
		rate := 0.0
		if res.ChecksTotal > 0 {
			rate = float64(res.ChecksPassed) / float64(res.ChecksTotal)
		}
		if res.ChecksTotal == 0 || rate < *j.ChecksRateMin {
			c.fail("run.checks_rate_min", "check の成功率が %.1f%%（%d / %d）です．%.0f%% 以上にしてください",
				rate*100, res.ChecksPassed, res.ChecksTotal, *j.ChecksRateMin*100)
		}
	}
	log := res.Log()
	for _, s := range j.LogMustNotContain {
		if strings.Contains(log, s) {
			c.fail("run.log_must_not_contain", "k6 のログに %q が出ています", s)
		}
	}
	for _, rc := range j.Requests {
		c.requests(rc, res)
	}
	for _, tc := range j.Thresholds {
		c.threshold(tc, res)
	}
}

func (c *checker) requests(rc exercise.RunRequestCond, res *runner.Result) {
	var re *regexp.Regexp
	if rc.URLRegex != "" {
		re, _ = regexp.Compile(rc.URLRegex)
	}
	n := 0
	for _, g := range res.Requests {
		t := g.Tags
		if rc.URLContains != "" && !strings.Contains(t["url"], rc.URLContains) {
			continue
		}
		if re != nil && !re.MatchString(t["url"]) {
			continue
		}
		if rc.Method != "" && !strings.EqualFold(t["method"], rc.Method) {
			continue
		}
		if rc.Status != 0 && t["status"] != strconv.Itoa(rc.Status) {
			continue
		}
		match := true
		for k, v := range rc.Tags {
			if t[k] != v {
				match = false
			}
		}
		if match {
			n += g.Count
		}
	}
	need := max(rc.Min, 1)
	if n < need {
		c.fail("run.requests", "%s が %d 件です（%d 件以上が必要）", describeRequest(rc), n, need)
	}
}

func describeRequest(rc exercise.RunRequestCond) string {
	var parts []string
	if rc.Method != "" {
		parts = append(parts, rc.Method)
	}
	switch {
	case rc.URLContains != "":
		parts = append(parts, rc.URLContains)
	case rc.URLRegex != "":
		parts = append(parts, "URL が "+rc.URLRegex+" に合う")
	}
	s := strings.Join(parts, " ") + " へのリクエスト"
	if rc.Status != 0 {
		s += "のうち状態コード " + strconv.Itoa(rc.Status) + " のもの"
	}
	if len(rc.Tags) > 0 {
		var kv []string
		for k, v := range rc.Tags {
			kv = append(kv, k+": '"+v+"'")
		}
		sort.Strings(kv)
		s += "（タグ { " + strings.Join(kv, ", ") + " } 付き）"
	}
	return s
}

// threshold は閾値1つについての条件を確かめる．候補のどれか1つがすべてを満たせばよい．
func (c *checker) threshold(tc exercise.ThresholdCond, res *runner.Result) {
	var cands []runner.ThresholdResult
	label := ""
	if tc.Metric != "" {
		key := runner.ParseMetricName(tc.Metric).Key()
		label = "閾値 " + tc.Metric
		for _, t := range res.Thresholds {
			if runner.ParseMetricName(t.Name).Key() == key {
				cands = append(cands, t)
			}
		}
	} else {
		label = tc.URLContains + " だけを測るタグ付き閾値"
		for _, t := range res.Thresholds {
			if t.Tagged && runner.ParseMetricName(t.Name).Base == "http_req_duration" && onlyURL(t, tc.URLContains) {
				cands = append(cands, t)
			}
		}
	}
	if tc.Expr != "" {
		label += "（式 " + tc.Expr + "）"
		var f []runner.ThresholdResult
		for _, t := range cands {
			if runner.NormalizeExpr(t.Expr) == runner.NormalizeExpr(tc.Expr) {
				f = append(f, t)
			}
		}
		cands = f
	}
	if len(cands) == 0 {
		if tc.URLContains != "" {
			c.fail("run.thresholds", "%s がありません．http_req_duration にタグで絞った閾値を置き，測定件数が1件以上になるようにしてください", label)
		} else {
			c.fail("run.thresholds", "%s がありません", label)
		}
		return
	}
	var why string
	for _, t := range cands {
		why = thresholdProblem(tc, t)
		if why == "" {
			return
		}
	}
	c.fail("run.thresholds", "%s: %s", label, why)
}

func onlyURL(t runner.ThresholdResult, sub string) bool {
	if t.Count == 0 {
		return false
	}
	for u := range t.URLs {
		if !strings.Contains(u, sub) {
			return false
		}
	}
	return true
}

func thresholdProblem(tc exercise.ThresholdCond, t runner.ThresholdResult) string {
	if t.Count < tc.MinCount {
		return fmt.Sprintf("測定件数が %d 件です（%d 件以上が必要）", t.Count, tc.MinCount)
	}
	if tc.OK != nil && t.OK != *tc.OK {
		if *tc.OK {
			return fmt.Sprintf("'%s' が不合格です（%s）．合格するようにしてください", t.Expr, t.Actual)
		}
		return fmt.Sprintf("'%s' が合格しています（%s）．劣化を検出できていません", t.Expr, t.Actual)
	}
	if tc.MaxMS != nil {
		e, err := runner.ParseExpr(t.Expr)
		if err != nil || (e.Op != "<" && e.Op != "<=") || e.Agg == "count" || e.Agg == "rate" || e.Value > *tc.MaxMS {
			return fmt.Sprintf("'%s' の上限が緩すぎます．時間の上限を %.0fms 以下にしてください", t.Expr, *tc.MaxMS)
		}
	}
	return ""
}

func (c *checker) hints(v Verdict) []string {
	res := c.in.Result
	var log string
	tagged := false
	statuses := map[string]bool{}
	if res != nil {
		log = res.Log()
		if res.Inspect != nil {
			for name := range res.Inspect.Thresholds {
				if len(runner.ParseMetricName(name).Tags) > 0 {
					tagged = true
				}
			}
		}
		for _, g := range res.Requests {
			statuses[g.Tags["status"]] = true
		}
	}
	var out []string
	for _, h := range c.in.Exercise.Hints {
		var hit bool
		switch h.When.Kind {
		case "zero_sample_threshold":
			hit = v.ZeroSample
		case "only_aggregate_threshold":
			hit = res != nil && res.Inspect != nil && !tagged
		case "exit_code":
			hit = res != nil && strconv.Itoa(res.ExitCode) == h.When.Value
		case "log_contains":
			hit = h.When.Value != "" && strings.Contains(log, h.When.Value)
		case "static_fail":
			hit = c.failed("static." + h.When.Value)
		case "fail":
			hit = c.failed(h.When.Value)
		case "status":
			hit = statuses[h.When.Value]
		case "always":
			hit = true
		}
		if hit {
			out = append(out, h.Text)
		}
	}
	return out
}
