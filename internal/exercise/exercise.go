// Package exercise は exercises/ に置いた問題ファイル（1問1ファイルの YAML）を読み込む．
package exercise

import (
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Exercise は問題ファイル1つ分．
type Exercise struct {
	ID          string     `yaml:"id" json:"id"`
	Level       string     `yaml:"level" json:"level"`
	Title       string     `yaml:"title" json:"title"`
	Scenario    string     `yaml:"scenario" json:"scenario"` // 「こういう状況では」の1行．レシピ集に出す
	Minutes     int        `yaml:"minutes" json:"minutes"`
	Optional    bool       `yaml:"optional" json:"optional"`
	Doc         StringList `yaml:"doc" json:"doc"`
	Type        string     `yaml:"type" json:"type"`
	Statement   string     `yaml:"statement" json:"statement"`
	Starter     string     `yaml:"starter" json:"starter,omitempty"`
	Solution    string     `yaml:"solution" json:"-"`
	Assets      []string   `yaml:"assets" json:"assets,omitempty"`
	Quiz        []Question `yaml:"quiz" json:"-"`
	Judge       *Judge     `yaml:"judge" json:"-"`
	Tips        []Tip      `yaml:"tips" json:"tips,omitempty"` // 問題文の下に出す段階的なヒント（採点の前から見られる）
	Hints       []Hint     `yaml:"hints" json:"-"`
	Explanation string     `yaml:"explanation" json:"-"`
}

// HasCode は k6 を実行して判定する部分を持つか．
func (e *Exercise) HasCode() bool { return e.Type == "code" }

// StringList は1つの文字列か文字列の配列を受け付ける．
type StringList []string

func (s *StringList) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.ScalarNode {
		*s = []string{n.Value}
		return nil
	}
	var l []string
	if err := n.Decode(&l); err != nil {
		return err
	}
	*s = l
	return nil
}

// Tip は問題文の下に出すヒント1つ．学習者が開いたときだけ見える．後ろのものほど答えに近くする．
type Tip struct {
	Title string `yaml:"title" json:"title"`
	Text  string `yaml:"text" json:"text"`
}

// Question は選択式の設問1つ．
type Question struct {
	Question string   `yaml:"question"`
	Code     string   `yaml:"code"` // 設問に添える k6 の出力やコード（任意）
	Choices  []Choice `yaml:"choices"`
}

// Choice は選択肢1つ．
type Choice struct {
	Text    string `yaml:"text"`
	Correct bool   `yaml:"correct"`
	Explain string `yaml:"explain"`
}

// Judge は判定条件．static（静的解析による採点）が主で，run（k6 での実機実行）は拡張機能．
type Judge struct {
	Static StaticJudge `yaml:"static"`
	Run    *RunJudge   `yaml:"run"`
}

// StaticJudge はコードを実行せずに確かめる条件．書いた条件をすべて満たしたとき合格とする．
// 実行すると誤りになると分かるもの（open() の位置，process の使用，測定0件の閾値など）は，
// allow_problems に書かない限り，それだけで不合格にする．
type StaticJudge struct {
	AllowProblems       []string        `yaml:"allow_problems"`
	Imports             []string        `yaml:"imports"`
	MustContain         []string        `yaml:"must_contain"`
	MustNotContain      []string        `yaml:"must_not_contain"`
	Options             map[string]any  `yaml:"options"`
	MinVUs              int             `yaml:"min_vus"`
	MaxVUs              int             `yaml:"max_vus"`
	MinStages           int             `yaml:"min_stages"`
	StagesEndAtZero     bool            `yaml:"stages_end_at_zero"`
	Spike               *SpikeCond      `yaml:"spike"`
	MinScenarios        int             `yaml:"min_scenarios"`
	Scenarios           []ScenarioCond  `yaml:"scenarios"`
	Requests            []RequestCond   `yaml:"requests"`
	Thresholds          []ThresholdCond `yaml:"thresholds"`
	ThresholdsUnchanged bool            `yaml:"thresholds_unchanged"`
	Checks              *ChecksCond     `yaml:"checks"`
	OpenInInit          bool            `yaml:"open_in_init"`
	Env                 []string        `yaml:"env"`
	SetupReturns        bool            `yaml:"setup_returns"`
	DefaultUsesData     bool            `yaml:"default_uses_data"`
	Sleep               bool            `yaml:"sleep"`
}

// SpikeCond は「短い時間で一気に VU を増やす段がある」という条件．
type SpikeCond struct {
	MinTarget int    `yaml:"min_target"`
	MaxRamp   string `yaml:"max_ramp"`
}

// ScenarioCond はシナリオ1つについての条件．Fields は options.scenarios の値と比べる．
type ScenarioCond struct {
	Name     string         `yaml:"name"`
	Executor string         `yaml:"executor"`
	Exec     string         `yaml:"exec"`
	Fields   map[string]any `yaml:"fields"`
}

// RequestCond は「条件に合うリクエストをコードに min か所以上書いた（max を書いたらそれ以下）」という条件．
type RequestCond struct {
	Method      string            `yaml:"method"`
	Path        string            `yaml:"path"`
	PathRegex   string            `yaml:"path_regex"`
	Query       string            `yaml:"query"`
	Tags        map[string]string `yaml:"tags"`
	Headers     map[string]string `yaml:"headers"` // 値は正規表現
	Body        string            `yaml:"body"`    // json, file, object
	In          string            `yaml:"in"`      // default, setup, 関数名
	UsesBaseURL bool              `yaml:"uses_base_url"`
	InLoop      bool              `yaml:"in_loop"`
	Min         int               `yaml:"min"`
	Max         *int              `yaml:"max"`
	Label       string            `yaml:"label"` // 満たさないときに出す説明
}

// ThresholdCond は閾値1つについての条件．metric で閾値の名前を直接指定するか，
// for_path で「タグで絞った http_req_duration の閾値のうち，測るリクエストがすべてそのパスのもの」を指定する．
// run（実機実行）では url_contains，ok，min_count も使える．
type ThresholdCond struct {
	Metric      string   `yaml:"metric"`
	ForPath     string   `yaml:"for_path"`
	URLContains string   `yaml:"url_contains"`
	Expr        string   `yaml:"expr"`
	OK          *bool    `yaml:"ok"`
	MinCount    int      `yaml:"min_count"`
	MaxMS       *float64 `yaml:"max_ms"`
}

// ChecksCond は check() についての条件．
type ChecksCond struct {
	Min      int    `yaml:"min"`       // check() の呼び出しの数
	MinNames int    `yaml:"min_names"` // 項目の数
	Status   []int  `yaml:"status"`    // 条件の中で比べている状態コード
	UsesJSON bool   `yaml:"uses_json"` // 条件の中で r.json() を使っている
	In       string `yaml:"in"`
}

// RunJudge は k6 run の結果を見る条件．
type RunJudge struct {
	ExitCode          *int            `yaml:"exit_code"`
	Thresholds        []ThresholdCond `yaml:"thresholds"`
	MinHTTPReqs       *int            `yaml:"min_http_reqs"`
	ChecksRateMin     *float64        `yaml:"checks_rate_min"`
	MinChecks         int             `yaml:"min_checks"`
	MinCheckNames     int             `yaml:"min_check_names"`
	LogMustNotContain []string        `yaml:"log_must_not_contain"`
	Requests          []RunRequestCond `yaml:"requests"`
}

// RunRequestCond は実機実行で「条件に合うリクエストを min 件以上送った」という条件．
type RunRequestCond struct {
	URLContains string            `yaml:"url_contains"`
	URLRegex    string            `yaml:"url_regex"`
	Method      string            `yaml:"method"`
	Status      int               `yaml:"status"`
	Tags        map[string]string `yaml:"tags"`
	Min         int               `yaml:"min"`
}

// Hint は不合格の理由ごとのヒント．
type Hint struct {
	When HintWhen `yaml:"when"`
	Text string   `yaml:"text"`
}

// HintWhen は `when: zero_sample_threshold` のような文字列か，
// `when: {exit_code: 0}` のような1組のキーと値．
type HintWhen struct {
	Kind  string
	Value string
}

func (h *HintWhen) UnmarshalYAML(n *yaml.Node) error {
	switch n.Kind {
	case yaml.ScalarNode:
		h.Kind = n.Value
		return nil
	case yaml.MappingNode:
		if len(n.Content) != 2 {
			return fmt.Errorf("line %d: when にはキーを1つだけ書いてください", n.Line)
		}
		h.Kind, h.Value = n.Content[0].Value, n.Content[1].Value
		return nil
	}
	return fmt.Errorf("line %d: when の形式が不正です", n.Line)
}

// Set は読み込んだ問題の集まり．ID の順に並ぶ．
type Set struct {
	List []*Exercise
	byID map[string]*Exercise
}

// Get は ID で問題を引く．
func (s *Set) Get(id string) *Exercise { return s.byID[id] }

// Load は fsys の basic/ と advanced/ にある *.yaml をすべて読み込む．
func Load(fsys fs.FS) (*Set, error) {
	set := &Set{byID: map[string]*Exercise{}}
	for _, dir := range []string{"basic", "advanced"} {
		files, err := fs.Glob(fsys, dir+"/*.yaml")
		if err != nil {
			return nil, err
		}
		for _, f := range files {
			b, err := fs.ReadFile(fsys, f)
			if err != nil {
				return nil, err
			}
			var e Exercise
			if err := yaml.Unmarshal(b, &e); err != nil {
				return nil, fmt.Errorf("%s: %w", f, err)
			}
			if err := e.validate(strings.TrimSuffix(path.Base(f), ".yaml"), fsys); err != nil {
				return nil, fmt.Errorf("%s: %w", f, err)
			}
			if set.byID[e.ID] != nil {
				return nil, fmt.Errorf("%s: id %s が重複しています", f, e.ID)
			}
			set.byID[e.ID] = &e
			set.List = append(set.List, &e)
		}
	}
	sort.SliceStable(set.List, func(i, j int) bool {
		a, b := set.List[i], set.List[j]
		if a.Level != b.Level {
			return a.Level == "basic"
		}
		return a.ID < b.ID
	})
	return set, nil
}

func (e *Exercise) validate(name string, fsys fs.FS) error {
	var missing []string
	need := func(ok bool, key string) {
		if !ok {
			missing = append(missing, key)
		}
	}
	need(e.ID != "", "id")
	need(e.Title != "", "title")
	need(e.Minutes > 0, "minutes")
	need(e.Statement != "", "statement")
	need(e.Explanation != "", "explanation")
	need(e.Level == "basic" || e.Level == "advanced", "level（basic か advanced）")
	need(e.Type == "code" || e.Type == "quiz", "type（code か quiz）")
	if e.Level == "basic" {
		need(len(e.Doc) > 0, "doc")
	}
	if e.Type == "code" {
		need(e.Starter != "", "starter")
		need(e.Judge != nil, "judge")
	}
	if e.Type == "quiz" {
		need(len(e.Quiz) > 0, "quiz")
	}
	if len(missing) > 0 {
		return fmt.Errorf("必須のキーがありません: %s", strings.Join(missing, ", "))
	}
	if e.ID != name {
		return fmt.Errorf("id %q がファイル名 %q と一致しません", e.ID, name)
	}
	for i, q := range e.Quiz {
		n := 0
		for _, c := range q.Choices {
			if c.Correct {
				n++
			}
		}
		if n != 1 {
			return fmt.Errorf("quiz[%d]: 正解の選択肢はちょうど1つにしてください", i)
		}
	}
	for _, a := range e.Assets {
		if _, err := fs.Stat(fsys, path.Join("assets", a)); err != nil {
			return fmt.Errorf("assets/%s がありません", a)
		}
	}
	return nil
}
