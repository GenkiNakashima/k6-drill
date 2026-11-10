package judge

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"k6-drill/internal/analyze"
	"k6-drill/internal/exercise"
	"k6-drill/internal/runner"
)

// StaticVerdict は静的解析による採点の結果．
type StaticVerdict struct {
	Passed   bool      `json:"passed"`
	Failures []Failure `json:"failures"`
	Hints    []string  `json:"hints"`
	Analysis *Analysis `json:"analysis"`
}

// Analysis は「コードから読み取ったこと」として画面に出すもの．
type Analysis struct {
	Load       *Load              `json:"load"`
	Requests   []RequestView      `json:"requests"`
	Thresholds []ThresholdView    `json:"thresholds"`
	Checks     []analyze.Check    `json:"checks"`
	Problems   []analyze.Problem  `json:"problems"`
	SyntaxErr  string             `json:"syntax_error,omitempty"`
	model      *analyze.Model
}

// Load は負荷のかけ方の要約．
type Load struct {
	MaxVUs    int            `json:"max_vus"`
	Duration  string         `json:"duration"`
	Stages    []runner.Stage `json:"stages"`
	Scenarios []ScenarioView `json:"scenarios"`
}

// ScenarioView はシナリオ1つの要約．
type ScenarioView struct {
	Name     string `json:"name"`
	Executor string `json:"executor"`
	Exec     string `json:"exec"`
	MaxVUs   int    `json:"max_vus"`
}

// RequestView はリクエスト1か所の要約．
type RequestView struct {
	analyze.Request
	Place string `json:"place"`
}

// ThresholdView は閾値1つの要約．Targets はその閾値が測るリクエスト．
type ThresholdView struct {
	Name    string   `json:"name"`
	Exprs   []string `json:"exprs"`
	Tagged  bool     `json:"tagged"`
	Targets []string `json:"targets"`
	Certain bool     `json:"certain"`
	Zero    bool     `json:"zero"`
	All     bool     `json:"all"` // タグで絞っていない（すべてのリクエストを測る）
}

// Summarize は Model を画面に出す形にする．
func Summarize(m *analyze.Model) *Analysis {
	a := &Analysis{model: m, Checks: m.Checks, Problems: m.Problems}
	if a.Checks == nil {
		a.Checks = []analyze.Check{}
	}
	if a.Problems == nil {
		a.Problems = []analyze.Problem{}
	}
	if m.Exec != nil {
		l := &Load{MaxVUs: m.Exec.MaxVUs, Stages: m.Exec.AllStages()}
		if m.Exec.Duration > 0 {
			l.Duration = m.Exec.Duration.String()
		}
		for _, s := range m.Exec.Scenarios {
			l.Scenarios = append(l.Scenarios, ScenarioView{s.Name, s.Executor, s.Exec, s.MaxVUs})
		}
		a.Load = l
	} else {
		a.Load = &Load{MaxVUs: 1, Scenarios: []ScenarioView{{"default", "（options なし：1 VU で1回）", "default", 1}}}
	}
	for _, r := range m.Requests {
		a.Requests = append(a.Requests, RequestView{r, r.Place()})
	}
	for _, t := range m.Thresholds {
		v := ThresholdView{Name: t.Name, Exprs: t.Exprs, Tagged: len(t.Metric.Tags) > 0}
		if !v.Tagged {
			v.All, v.Certain = true, true
		} else {
			res := m.Measures(t)
			v.Certain = res.Certain
			for _, i := range res.Requests {
				r := m.Requests[i]
				v.Targets = append(v.Targets, fmt.Sprintf("%s %s（%d 行目）", r.Method, r.Path, r.Line))
			}
			v.Zero = res.Certain && len(res.Requests) == 0 && strings.HasPrefix(t.Metric.Base, "http_req")
		}
		a.Thresholds = append(a.Thresholds, v)
	}
	return a
}

// StaticInput は静的な採点に必要なもの．Starter は thresholds_unchanged のときだけ使う．
type StaticInput struct {
	Exercise *exercise.Exercise
	Code     string
	Starter  *analyze.Model
}

type staticChecker struct {
	m        *analyze.Model
	j        exercise.StaticJudge
	failures []Failure
}

func (c *staticChecker) fail(code, format string, args ...any) {
	c.failures = append(c.failures, Failure{Code: code, Message: fmt.Sprintf(format, args...)})
}

// EvaluateStatic はコードを実行せずに採点する．
func EvaluateStatic(in StaticInput) StaticVerdict {
	j := in.Exercise.Judge.Static
	m, err := analyze.Parse(in.Code)
	if err != nil {
		var se *analyze.SyntaxError
		msg := err.Error()
		if errors.As(err, &se) {
			msg = se.Err.Error()
		}
		v := StaticVerdict{
			Failures: []Failure{{Code: "syntax", Message: "コードを構文解析できません（JavaScript として誤りがあります）: " + msg}},
			Analysis: &Analysis{SyntaxErr: msg, Checks: []analyze.Check{}, Problems: []analyze.Problem{}},
		}
		v.Hints = staticHints(in.Exercise, nil, v.Failures)
		return v
	}
	c := &staticChecker{m: m, j: j}
	c.problems()
	c.text(in.Code)
	c.load()
	c.requests()
	c.thresholds(in.Starter)
	c.checks()
	c.misc()

	v := StaticVerdict{Passed: len(c.failures) == 0, Failures: c.failures, Analysis: Summarize(m)}
	if v.Failures == nil {
		v.Failures = []Failure{}
	}
	if !v.Passed {
		v.Hints = staticHints(in.Exercise, m, v.Failures)
	}
	return v
}

func (c *staticChecker) problems() {
	allow := map[string]bool{}
	for _, a := range c.j.AllowProblems {
		allow[a] = true
	}
	for _, p := range c.m.Problems {
		if !allow[p.Code] {
			msg := p.Message
			if p.Line > 0 {
				msg = fmt.Sprintf("%d 行目：%s", p.Line, msg)
			}
			c.failures = append(c.failures, Failure{Code: "problem." + p.Code, Message: msg})
		}
	}
}

func (c *staticChecker) text(code string) {
	for _, mod := range c.j.Imports {
		found := false
		for _, v := range c.m.Imports {
			found = found || v == mod
		}
		if !found {
			c.fail("imports", "`%s` を import していません", mod)
		}
	}
	for _, p := range c.j.MustContain {
		if re, err := regexp.Compile(p); err == nil && !re.MatchString(code) {
			c.fail("must_contain", "コードに `%s` に当たる書き方がありません", unescape(p))
		}
	}
	for _, p := range c.j.MustNotContain {
		if re, err := regexp.Compile(p); err == nil && re.MatchString(code) {
			c.fail("must_not_contain", "コードに使ってはいけない書き方 `%s` が残っています", unescape(p))
		}
	}
}

func (c *staticChecker) load() {
	j, m := c.j, c.m
	keys := make([]string, 0, len(j.Options))
	for k := range j.Options {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		got, ok := m.Options[k]
		if !ok || !runner.SameValue(got, j.Options[k]) {
			c.fail("options", "options.%s を %v にしてください（いまは %s）", k, j.Options[k], display(got, ok))
		}
	}
	maxVUs := 1
	if m.Exec != nil {
		maxVUs = m.Exec.MaxVUs
	}
	if j.MinVUs > 0 && maxVUs < j.MinVUs {
		c.fail("min_vus", "VU の最大が %d です．%d 以上にしてください", maxVUs, j.MinVUs)
	}
	if j.MaxVUs > 0 && maxVUs > j.MaxVUs {
		c.fail("max_vus", "VU の最大が %d です．%d 以下にしてください", maxVUs, j.MaxVUs)
	}
	var stages []runner.Stage
	if m.Exec != nil {
		stages = m.Exec.AllStages()
	}
	if j.MinStages > 0 && len(stages) < j.MinStages {
		c.fail("min_stages", "stages が %d 段です．%d 段以上にしてください", len(stages), j.MinStages)
	}
	if j.StagesEndAtZero && (len(stages) == 0 || stages[len(stages)-1].Target != 0) {
		c.fail("stages_end_at_zero", "最後の段の target を 0 にして，負荷を下げて終わるようにしてください")
	}
	if s := j.Spike; s != nil && !hasSpike(stages, s) {
		c.fail("spike", "%s 以内で %d VU 以上まで一気に上げる段がありません", s.MaxRamp, s.MinTarget)
	}
	var scenarios map[string]any
	if m.Options != nil {
		scenarios, _ = m.Options["scenarios"].(map[string]any)
	}
	if j.MinScenarios > 0 && len(scenarios) < j.MinScenarios {
		c.fail("min_scenarios", "options.scenarios のシナリオが %d 個です．%d 個以上書いてください", len(scenarios), j.MinScenarios)
	}
	for _, sc := range j.Scenarios {
		if !scenarioMatches(scenarios, sc) {
			c.fail("scenarios", "%s", describeScenario(sc))
		}
	}
}

func hasSpike(stages []runner.Stage, s *exercise.SpikeCond) bool {
	maxRamp, _ := time.ParseDuration(s.MaxRamp)
	prev := 0
	for _, st := range stages {
		if st.Target >= s.MinTarget && st.Duration <= maxRamp && prev*5 <= st.Target {
			return true
		}
		prev = st.Target
	}
	return false
}

func scenarioMatches(scenarios map[string]any, sc exercise.ScenarioCond) bool {
	for name, v := range scenarios {
		cfg, _ := v.(map[string]any)
		if sc.Name != "" && name != sc.Name {
			continue
		}
		exec, _ := cfg["exec"].(string)
		if exec == "" {
			exec = "default"
		}
		if sc.Exec != "" && exec != sc.Exec {
			continue
		}
		if sc.Executor != "" && cfg["executor"] != sc.Executor {
			continue
		}
		ok := true
		for k, want := range sc.Fields {
			if got, has := cfg[k]; !has || !runner.SameValue(got, want) {
				ok = false
			}
		}
		if ok {
			return true
		}
	}
	return false
}

func describeScenario(sc exercise.ScenarioCond) string {
	var parts []string
	if sc.Name != "" {
		parts = append(parts, "名前 "+sc.Name)
	}
	if sc.Executor != "" {
		parts = append(parts, "executor '"+sc.Executor+"'")
	}
	if sc.Exec != "" {
		parts = append(parts, "exec '"+sc.Exec+"'")
	}
	keys := make([]string, 0, len(sc.Fields))
	for k := range sc.Fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s: %v", k, sc.Fields[k]))
	}
	return "シナリオ（" + strings.Join(parts, "，") + "）がありません"
}

func (c *staticChecker) requests() {
	for _, rc := range c.j.Requests {
		n := 0
		for _, r := range c.m.Requests {
			if requestMatches(rc, r) {
				n++
			}
		}
		label := rc.Label
		if label == "" {
			label = describeStaticRequest(rc)
		}
		switch {
		case rc.Max != nil && n > *rc.Max:
			if *rc.Max == 0 {
				c.fail("requests", "%sがあります．ここには書かないでください", label)
			} else {
				c.fail("requests", "%sが %d か所あります（%d か所以下にしてください）", label, n, *rc.Max)
			}
		case rc.Max == nil && n < max(rc.Min, 1):
			c.fail("requests", "%sがありません", label)
		}
	}
}

func requestMatches(rc exercise.RequestCond, r analyze.Request) bool {
	if rc.Method != "" && !strings.EqualFold(rc.Method, r.Method) {
		return false
	}
	if rc.Path != "" && rc.Path != r.Path {
		return false
	}
	if rc.PathRegex != "" {
		if ok, _ := regexp.MatchString(rc.PathRegex, r.Path); !ok {
			return false
		}
	}
	if rc.Query != "" && !strings.Contains(r.Query, rc.Query) {
		return false
	}
	for k, v := range rc.Tags {
		if r.Tags[k] != v {
			return false
		}
	}
	for k, pat := range rc.Headers {
		val, ok := headerValue(r.Headers, k)
		if !ok {
			return false
		}
		if m, _ := regexp.MatchString(pat, val); !m {
			return false
		}
	}
	if rc.Body != "" && rc.Body != r.Body {
		return false
	}
	if rc.In != "" && rc.In != r.Func {
		return false
	}
	if rc.UsesBaseURL && !r.UsesBaseURL {
		return false
	}
	if rc.InLoop && !r.InLoop {
		return false
	}
	return true
}

// headerValue はヘッダーの名前を大文字と小文字を区別せずに引く（HTTP の決まりと同じ）．
func headerValue(h map[string]string, key string) (string, bool) {
	for k, v := range h {
		if strings.EqualFold(k, key) {
			return v, true
		}
	}
	return "", false
}

func describeStaticRequest(rc exercise.RequestCond) string {
	var b strings.Builder
	switch rc.In {
	case "":
	case "default":
		b.WriteString("default 関数の中に，")
	default:
		b.WriteString(rc.In + "() の中に，")
	}
	method := rc.Method
	if method == "" {
		method = "HTTP"
	}
	path := rc.Path
	if path == "" && rc.PathRegex != "" {
		path = "（パスが " + rc.PathRegex + " に合う）"
	}
	b.WriteString(method + " " + path)
	if rc.Query != "" {
		b.WriteString("?" + rc.Query)
	}
	b.WriteString(" のリクエスト")
	var extra []string
	if len(rc.Tags) > 0 {
		extra = append(extra, "タグ "+kvString(rc.Tags))
	}
	if len(rc.Headers) > 0 {
		keys := make([]string, 0, len(rc.Headers))
		for k := range rc.Headers {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		extra = append(extra, "ヘッダー "+strings.Join(keys, "，"))
	}
	switch rc.Body {
	case "json":
		extra = append(extra, "JSON.stringify() した本文")
	case "file":
		extra = append(extra, "http.file() を含む multipart の本文")
	}
	if rc.UsesBaseURL {
		extra = append(extra, "接続先は __ENV.BASE_URL")
	}
	if rc.InLoop {
		extra = append(extra, "繰り返しの中")
	}
	if len(extra) > 0 {
		b.WriteString("（" + strings.Join(extra, "，") + "）")
	}
	return b.String()
}

func kvString(m map[string]string) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = k + ": '" + m[k] + "'"
	}
	return "{ " + strings.Join(parts, ", ") + " }"
}

func (c *staticChecker) thresholds(starter *analyze.Model) {
	m := c.m
	for _, tc := range c.j.Thresholds {
		var cands []analyze.Threshold
		var label string
		if tc.Metric != "" {
			key := runner.ParseMetricName(tc.Metric).Key()
			label = "閾値 `" + tc.Metric + "`"
			for _, t := range m.Thresholds {
				if t.Metric.Key() == key {
					cands = append(cands, t)
				}
			}
		} else {
			label = tc.ForPath + " だけを測る，タグで絞った閾値"
			for _, t := range m.Thresholds {
				if len(t.Metric.Tags) > 0 && t.Metric.Base == "http_req_duration" && c.measuresOnly(t, tc.ForPath) {
					cands = append(cands, t)
				}
			}
		}
		if len(cands) == 0 {
			if tc.ForPath != "" {
				c.fail("thresholds", "%sがありません．リクエストにタグを付け，`'http_req_duration{name:...}'` のように，そのリクエストだけを測る閾値を置いてください", label)
			} else {
				c.fail("thresholds", "%sがありません", label)
			}
			continue
		}
		ok := false
		var why string
		for _, t := range cands {
			why = exprProblem(tc, t)
			if why == "" {
				ok = true
				break
			}
		}
		if !ok {
			c.fail("thresholds", "%s: %s", label, why)
		}
	}
	if c.j.ThresholdsUnchanged {
		switch {
		case starter == nil:
			c.fail("thresholds_unchanged", "初期コードの閾値を読めませんでした")
		case modelSig(m) != modelSig(starter):
			c.fail("thresholds_unchanged", "閾値（options.thresholds）が初期コードから変わっています．閾値は変えずに直してください")
		}
	}
}

// measuresOnly は閾値が測るリクエストが1つ以上あり，すべて path へのものか．
func (c *staticChecker) measuresOnly(t analyze.Threshold, path string) bool {
	res := c.m.Measures(t)
	if len(res.Requests) == 0 {
		return false
	}
	for _, i := range res.Requests {
		if c.m.Requests[i].Path != path {
			return false
		}
	}
	return true
}

func exprProblem(tc exercise.ThresholdCond, t analyze.Threshold) string {
	if tc.Expr != "" {
		for _, e := range t.Exprs {
			if runner.NormalizeExpr(e) == runner.NormalizeExpr(tc.Expr) {
				return ""
			}
		}
		return "式 `" + tc.Expr + "` がありません（いまは " + quoteList(t.Exprs) + "）"
	}
	if tc.MaxMS != nil {
		for _, ex := range t.Exprs {
			e, err := runner.ParseExpr(ex)
			if err == nil && (e.Op == "<" || e.Op == "<=") && e.Agg != "count" && e.Agg != "rate" && e.Value <= *tc.MaxMS {
				return ""
			}
		}
		return fmt.Sprintf("時間の上限が %.0fms より緩い（または上限がない）です．`'p(95)<...'` の数値を %.0f 以下にしてください", *tc.MaxMS, *tc.MaxMS)
	}
	if len(t.Exprs) == 0 {
		return "式がありません"
	}
	return ""
}

func quoteList(l []string) string {
	if len(l) == 0 {
		return "なし"
	}
	q := make([]string, len(l))
	for i, s := range l {
		q[i] = "`" + s + "`"
	}
	return strings.Join(q, "，")
}

func modelSig(m *analyze.Model) string {
	th := map[string][]string{}
	for _, t := range m.Thresholds {
		th[t.Name] = t.Exprs
	}
	return thresholdSig(th)
}

var statusRe = regexp.MustCompile(`status\s*(?:===|==)\s*(\d{3})|(\d{3})\s*(?:===|==)\s*\w+\.status`)

func (c *staticChecker) checks() {
	cc := c.j.Checks
	if cc == nil {
		return
	}
	var list []analyze.Check
	for _, ch := range c.m.Checks {
		if cc.In == "" || ch.Func == cc.In {
			list = append(list, ch)
		}
	}
	names, statuses, usesJSON := 0, map[int]bool{}, false
	for _, ch := range list {
		names += len(ch.Names)
		for _, b := range ch.Bodies {
			for _, mm := range statusRe.FindAllStringSubmatch(b, -1) {
				n, _ := strconv.Atoi(mm[1] + mm[2])
				statuses[n] = true
			}
			usesJSON = usesJSON || strings.Contains(b, ".json(")
		}
	}
	if len(list) < max(cc.Min, 1) {
		c.fail("checks", "check() が %d か所です（%d か所以上が必要）", len(list), max(cc.Min, 1))
	}
	if cc.MinNames > 0 && names < cc.MinNames {
		c.fail("checks", "check の項目（`'名前': (r) => 条件`）が %d 個です（%d 個以上が必要）", names, cc.MinNames)
	}
	for _, s := range cc.Status {
		if !statuses[s] {
			c.fail("checks", "状態コードが %d であることを確かめる check（`r.status === %d`）がありません", s, s)
		}
	}
	if cc.UsesJSON && !usesJSON {
		c.fail("checks", "応答の本文を `r.json()` で読んで確かめる check がありません")
	}
}

func (c *staticChecker) misc() {
	j, m := c.j, c.m
	if j.OpenInInit {
		switch {
		case len(m.Opens) == 0:
			c.fail("open_in_init", "open() を呼んでいません")
		default:
			for _, o := range m.Opens {
				if o.Func != "" && !m.HasProblem("open_in_vu") {
					c.fail("open_in_init", "open() が %s の中にあります", analyzePlace(o.Func))
				}
			}
		}
	}
	for _, name := range j.Env {
		found := false
		for _, e := range m.EnvVars {
			found = found || e == name
		}
		if !found {
			c.fail("env", "接続先などを `__ENV.%s` から読んでいません", name)
		}
	}
	if j.SetupReturns && !m.SetupReturns {
		c.fail("setup_returns", "`export function setup()` で値を return していません．setup() の戻り値が default 関数の引数になります")
	}
	if j.DefaultUsesData && m.DefaultParams == 0 {
		c.fail("default_uses_data", "default 関数が引数を受け取っていません．`export default function (data) { ... }` のように setup() の戻り値を受け取ります")
	}
	if j.Sleep && len(m.Sleeps) == 0 {
		c.fail("sleep", "sleep() がありません．利用者が画面を読む時間（think time）を `sleep(1)` のように入れます")
	}
}

func analyzePlace(f string) string {
	return analyze.Request{Func: f}.Place()
}

func display(v any, ok bool) string {
	if !ok || v == nil {
		return "未設定"
	}
	if u, isU := v.(analyze.Unknown); isU {
		return "`" + u.Src + "`（静的には決まらない値）"
	}
	return fmt.Sprint(v)
}

// staticHints は満たさなかった条件に合うヒントを選ぶ．
func staticHints(ex *exercise.Exercise, m *analyze.Model, failures []Failure) []string {
	failed := func(code string) bool {
		for _, f := range failures {
			if f.Code == code || strings.TrimPrefix(f.Code, "problem.") == code && strings.HasPrefix(f.Code, "problem.") {
				return true
			}
		}
		return false
	}
	var out []string
	for _, h := range ex.Hints {
		var hit bool
		switch h.When.Kind {
		case "zero_sample_threshold":
			hit = m != nil && m.HasProblem("zero_sample")
		case "only_aggregate_threshold":
			hit = m != nil && !m.TaggedThresholds()
		case "fail", "static_fail":
			hit = failed(h.When.Value)
		case "problem":
			hit = m != nil && m.HasProblem(h.When.Value)
		case "syntax":
			hit = failed("syntax")
		case "always":
			hit = true
		}
		if hit {
			out = appendUniqStr(out, h.Text)
		}
	}
	return out
}

func appendUniqStr(l []string, s string) []string {
	for _, x := range l {
		if x == s {
			return l
		}
	}
	return append(l, s)
}
