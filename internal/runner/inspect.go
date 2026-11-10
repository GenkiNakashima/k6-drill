package runner

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Inspect は `k6 inspect` で解決した options のうち，判定と実行前の確認に使うもの．
type Inspect struct {
	Raw        map[string]any      `json:"-"`
	MaxVUs     int                 `json:"max_vus"`
	Duration   time.Duration       `json:"-"` // 時間で区切るシナリオの終わり（gracefulStop は含めない）
	Scenarios  []Scenario          `json:"scenarios"`
	Stages     []Stage             `json:"stages,omitempty"`
	Thresholds map[string][]string `json:"thresholds"` // 閾値の名前 → 式
}

// Scenario は解決後のシナリオ1つ．
type Scenario struct {
	Name     string  `json:"name"`
	Executor string  `json:"executor"`
	Exec     string  `json:"exec"`
	MaxVUs   int     `json:"max_vus"`
	Stages   []Stage `json:"stages,omitempty"`
	end      time.Duration
}

// Stage は stages の1段．
type Stage struct {
	Duration time.Duration `json:"duration"`
	Target   int           `json:"target"`
}

// ParseInspect は k6 inspect の JSON を読む．
// --execution-requirements を付けたときの maxVUs があればそれを使い，なければ options から求める．
func ParseInspect(b []byte) (*Inspect, error) {
	var raw map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, fmt.Errorf("k6 inspect の出力を JSON として読めません: %w", err)
	}
	in := &Inspect{Raw: raw, Thresholds: map[string][]string{}}
	in.Stages = parseStages(raw["stages"])

	if th, ok := raw["thresholds"].(map[string]any); ok {
		for name, v := range th {
			in.Thresholds[name] = thresholdExprs(v)
		}
	}

	if sc, ok := raw["scenarios"].(map[string]any); ok && len(sc) > 0 {
		for name, v := range sc {
			m, _ := v.(map[string]any)
			in.Scenarios = append(in.Scenarios, parseScenario(name, m))
		}
		sort.Slice(in.Scenarios, func(i, j int) bool { return in.Scenarios[i].Name < in.Scenarios[j].Name })
	} else {
		in.Scenarios = []Scenario{deriveScenario(raw, in.Stages)}
	}

	sum := 0
	for _, s := range in.Scenarios {
		sum += s.MaxVUs
		if s.end > in.Duration {
			in.Duration = s.end
		}
	}
	in.MaxVUs = sum
	if n, ok := num(raw["maxVUs"]); ok && n > 0 {
		in.MaxVUs = int(n)
	}
	return in, nil
}

// AllStages は options.stages と ramping-vus のシナリオの stages をまとめて返す．
func (in *Inspect) AllStages() []Stage {
	if len(in.Stages) > 0 {
		return in.Stages
	}
	for _, s := range in.Scenarios {
		if len(s.Stages) > 0 {
			return s.Stages
		}
	}
	return nil
}

func thresholdExprs(v any) []string {
	var out []string
	switch t := v.(type) {
	case []any:
		for _, e := range t {
			switch x := e.(type) {
			case string:
				out = append(out, x)
			case map[string]any:
				if s, ok := x["threshold"].(string); ok {
					out = append(out, s)
				}
			}
		}
	case map[string]any:
		if l, ok := t["thresholds"]; ok {
			return thresholdExprs(l)
		}
	case string:
		out = append(out, t)
	}
	return out
}

func deriveScenario(raw map[string]any, stages []Stage) Scenario {
	s := Scenario{Name: "default", Exec: "default"}
	vus := 1
	if n, ok := num(raw["vus"]); ok && n > 0 {
		vus = int(n)
	}
	switch {
	case len(stages) > 0:
		s.Executor, s.Stages = "ramping-vus", stages
		s.MaxVUs = vus
		for _, st := range stages {
			s.end += st.Duration
			s.MaxVUs = max(s.MaxVUs, st.Target)
		}
	case dur(raw["duration"]) > 0:
		s.Executor, s.MaxVUs, s.end = "constant-vus", vus, dur(raw["duration"])
	default:
		s.Executor, s.MaxVUs = "shared-iterations", vus
	}
	return s
}

func parseScenario(name string, m map[string]any) Scenario {
	s := Scenario{Name: name}
	s.Executor, _ = m["executor"].(string)
	s.Exec, _ = m["exec"].(string)
	if s.Exec == "" {
		s.Exec = "default"
	}
	start := dur(m["startTime"])
	intOf := func(k string) int { n, _ := num(m[k]); return int(n) }
	switch s.Executor {
	case "ramping-vus":
		s.Stages = parseStages(m["stages"])
		s.MaxVUs = intOf("startVUs")
		var d time.Duration
		for _, st := range s.Stages {
			d += st.Duration
			s.MaxVUs = max(s.MaxVUs, st.Target)
		}
		s.end = start + d
	case "constant-vus":
		s.MaxVUs, s.end = intOf("vus"), start+dur(m["duration"])
	case "constant-arrival-rate":
		s.MaxVUs = max(intOf("preAllocatedVUs"), intOf("maxVUs"))
		s.end = start + dur(m["duration"])
	case "ramping-arrival-rate":
		s.MaxVUs = max(intOf("preAllocatedVUs"), intOf("maxVUs"))
		var d time.Duration
		for _, st := range parseStages(m["stages"]) {
			d += st.Duration
		}
		s.end = start + d
	default: // shared-iterations, per-vu-iterations, externally-controlled など
		s.MaxVUs = max(intOf("vus"), intOf("maxVUs"))
	}
	if s.MaxVUs == 0 {
		s.MaxVUs = 1
	}
	return s
}

func parseStages(v any) []Stage {
	l, _ := v.([]any)
	var out []Stage
	for _, e := range l {
		m, _ := e.(map[string]any)
		t, _ := num(m["target"])
		out = append(out, Stage{Duration: dur(m["duration"]), Target: int(t)})
	}
	return out
}

func num(v any) (float64, bool) {
	switch t := v.(type) {
	case float64:
		return t, true
	case int:
		return float64(t), true
	case int64:
		return float64(t), true
	case string:
		f, err := strconv.ParseFloat(t, 64)
		return f, err == nil
	}
	return 0, false
}

// dur は k6 の時間の表記（"20s"，"1m30s"，ミリ秒の数値）を読む．
func dur(v any) time.Duration {
	switch t := v.(type) {
	case string:
		if d, err := time.ParseDuration(strings.TrimSpace(t)); err == nil {
			return d
		}
		if f, err := strconv.ParseFloat(t, 64); err == nil {
			return time.Duration(f * float64(time.Millisecond))
		}
	case float64:
		return time.Duration(t * float64(time.Millisecond))
	}
	return 0
}

// SameValue は inspect の値と問題ファイルに書いた値が同じかを比べる．時間は表記の違いを無視する．
func SameValue(got, want any) bool {
	if g, ok := num(got); ok {
		if w, ok := num(want); ok {
			return g == w
		}
	}
	gs, gok := got.(string)
	ws, wok := want.(string)
	if gok && wok {
		if gd, wd := dur(gs), dur(ws); gd > 0 && gd == wd {
			return true
		}
		return gs == ws
	}
	return fmt.Sprint(got) == fmt.Sprint(want)
}
