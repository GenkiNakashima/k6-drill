// Package analyze は k6 のスクリプトを実行せずに構文解析し，採点に使う情報（options，リクエスト，
// check，閾値，open() の位置など）を取り出す．k6 がない PC でも採点できるようにするためのもの．
package analyze

import (
	"encoding/json"
	"reflect"
	"regexp"
	"sort"
	"strings"

	"github.com/grafana/sobek/ast"
	"github.com/grafana/sobek/parser"
	"github.com/grafana/sobek/token"

	"k6-drill/internal/runner"
)

// Request はコードの中の HTTP リクエスト1か所．
type Request struct {
	Method      string            `json:"method"`
	Path        string            `json:"path"`  // `/api/items/{}` のように，決まらない部分を {} にしたパス
	Query       string            `json:"query"` // `ms=100` など
	Host        string            `json:"host"`  // URL を文字列で直接書いたときのホスト
	UsesBaseURL bool              `json:"uses_base_url"`
	URLSrc      string            `json:"url_src"`
	Tags        map[string]string `json:"tags"`
	TagsUnknown bool              `json:"-"`
	Headers     map[string]string `json:"headers"`
	Body        string            `json:"body"` // json, object, file, string, other, ""
	Func        string            `json:"func"` // "" は init 段階
	InLoop      bool              `json:"in_loop"`
	Group       string            `json:"group,omitempty"`
	Line        int               `json:"line"`
}

// Place は Func を画面に出す形にする．
func (r Request) Place() string { return placeName(r.Func) }

func placeName(f string) string {
	switch f {
	case "":
		return "init 段階"
	case "default":
		return "default 関数"
	}
	return f + "()"
}

// Check はコードの中の check() 1か所．
type Check struct {
	Names  []string `json:"names"`
	Bodies []string `json:"bodies"` // 各項目の条件のコード
	Func   string   `json:"func"`
	Line   int      `json:"line"`
}

// Call は open() や sleep() など，位置だけを見る呼び出し．
type Call struct {
	Func string `json:"func"`
	Line int    `json:"line"`
}

// Threshold は options.thresholds の1つ．
type Threshold struct {
	Name   string            `json:"name"`
	Metric runner.MetricName `json:"-"`
	Exprs  []string          `json:"exprs"`
}

// Model はコードから読み取ったもの．
type Model struct {
	Exports         map[string]bool   `json:"exports"`
	Imports         map[string]string `json:"imports"` // 名前 → モジュール（import http from 'k6/http' なら http → k6/http）
	Options         map[string]any    `json:"options"`
	OptionsExported bool              `json:"options_exported"`
	HasOptions      bool              `json:"has_options"`
	Exec            *runner.Inspect   `json:"exec"`
	Thresholds      []Threshold       `json:"thresholds"`
	Requests        []Request         `json:"requests"`
	Checks          []Check           `json:"checks"`
	Opens           []Call            `json:"opens"`
	Sleeps          []Call            `json:"sleeps"`
	Groups          []string          `json:"groups"`
	EnvVars         []string          `json:"env_vars"` // __ENV.X の X
	ProcessUses     []int             `json:"process_uses"`
	SetupReturns    bool              `json:"setup_returns"`
	DefaultParams   int               `json:"default_params"`
	Problems        []Problem         `json:"problems"` // 実行すると誤りになると分かるもの

	code   string
	consts map[string]ast.Expression
	seen   map[ast.Node]bool // export default などで同じ節が二度現れるため
}

// Problem は実行する前に分かる誤り．
type Problem struct {
	Code    string `json:"code"`
	Line    int    `json:"line,omitempty"`
	Message string `json:"message"`
}

// SyntaxError は構文解析できなかったことを表す．
type SyntaxError struct{ Err error }

func (e *SyntaxError) Error() string { return e.Err.Error() }

// Parse はコードを ES モジュールとして構文解析し，Model を作る．
func Parse(code string) (*Model, error) {
	prog, err := parser.ParseFile(nil, "script.js", code, 0, parser.IsModule, parser.WithDisableSourceMaps)
	if err != nil {
		return nil, &SyntaxError{err}
	}
	m := &Model{
		Exports: map[string]bool{}, Imports: map[string]string{},
		code: code, consts: map[string]ast.Expression{}, seen: map[ast.Node]bool{},
	}
	m.collectTop(prog)
	m.walk(reflect.ValueOf(prog), ctx{})
	m.readOptions()
	m.findProblems()
	return m, nil
}

func (m *Model) src(n ast.Node) string {
	if n == nil || reflect.ValueOf(n).IsNil() {
		return ""
	}
	a, b := int(n.Idx0())-1, int(n.Idx1())-1
	if a < 0 || b > len(m.code) || a >= b {
		return ""
	}
	return m.code[a:b]
}

func (m *Model) line(n ast.Node) int {
	i := int(n.Idx0()) - 1
	if i < 0 || i > len(m.code) {
		return 0
	}
	return strings.Count(m.code[:i], "\n") + 1
}

// collectTop は最上位の import，export，const を集める．
func (m *Model) collectTop(prog *ast.Program) {
	for _, st := range prog.Body {
		switch s := st.(type) {
		case *ast.ImportDeclaration:
			mod := s.ModuleSpecifier.String()
			if s.FromClause != nil {
				mod = s.FromClause.ModuleSpecifier.String()
			}
			if c := s.ImportClause; c != nil {
				if c.ImportedDefaultBinding != nil {
					m.Imports[c.ImportedDefaultBinding.Name.String()] = mod
				}
				if c.NameSpaceImport != nil {
					m.Imports[c.NameSpaceImport.ImportedBinding.String()] = mod
				}
				if c.NamedImports != nil {
					for _, sp := range c.NamedImports.ImportsList {
						name := sp.Alias
						if name == "" {
							name = sp.IdentifierName
						}
						m.Imports[name.String()] = mod
					}
				}
			}
		case *ast.ExportDeclaration:
			m.collectExport(s)
		case *ast.LexicalDeclaration:
			m.bindings(s.List, false)
		case *ast.VariableStatement:
			m.bindings(s.List, false)
		}
	}
}

func (m *Model) bindings(list []*ast.Binding, exported bool) {
	for _, b := range list {
		id, ok := b.Target.(*ast.Identifier)
		if !ok {
			continue
		}
		name := id.Name.String()
		if b.Initializer != nil {
			m.consts[name] = b.Initializer
		}
		if exported {
			m.Exports[name] = true
		}
		if name == "options" {
			m.HasOptions = true
			m.OptionsExported = m.OptionsExported || exported
		}
	}
}

func (m *Model) collectExport(ex *ast.ExportDeclaration) {
	if ex.IsDefault {
		m.Exports["default"] = true
		return
	}
	if ex.HoistableDeclaration != nil && ex.HoistableDeclaration.FunctionDeclaration != nil {
		if n := ex.HoistableDeclaration.FunctionDeclaration.Function.Name; n != nil {
			m.Exports[n.Name.String()] = true
		}
	}
	if ex.LexicalDeclaration != nil {
		m.bindings(ex.LexicalDeclaration.List, true)
	}
	if ex.Variable != nil {
		m.bindings(ex.Variable.List, true)
	}
	if ex.ClassDeclaration != nil && ex.ClassDeclaration.Class.Name != nil {
		m.Exports[ex.ClassDeclaration.Class.Name.Name.String()] = true
	}
	if ex.NamedExports != nil {
		for _, sp := range ex.NamedExports.ExportsList {
			name := sp.Alias
			if name == "" {
				name = sp.IdentifierName
			}
			m.Exports[name.String()] = true
			if sp.IdentifierName.String() == "options" {
				m.OptionsExported = true
			}
		}
	}
}

type ctx struct {
	fn      string // 最上位の関数の名前．"" は init 段階
	depth   int    // 関数の入れ子の深さ
	loop    bool
	pending string // これから入る関数に付ける名前
	group   string
}

var nodeType = reflect.TypeOf((*ast.Node)(nil)).Elem()

// skipFields は同じ節を二度たどらないよう，ほかの場所の節を指し直しているだけの欄．
var skipFields = map[string]bool{"DeclarationList": true, "ImportEntries": true, "ExportEntries": true, "File": true}

func (m *Model) walk(v reflect.Value, c ctx) {
	switch v.Kind() {
	case reflect.Interface:
		if !v.IsNil() {
			m.walk(v.Elem(), c)
		}
	case reflect.Pointer:
		if v.IsNil() {
			return
		}
		if v.Type().Implements(nodeType) {
			if !m.visit(v.Interface().(ast.Node), &c) {
				return
			}
		}
		m.walk(v.Elem(), c)
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			f := v.Type().Field(i)
			if f.IsExported() && !skipFields[f.Name] {
				m.walk(v.Field(i), c)
			}
		}
	case reflect.Slice:
		for i := 0; i < v.Len(); i++ {
			m.walk(v.Index(i), c)
		}
	}
}

// visit は節に入るときに文脈を更新する．false を返すと子をたどらない（自分でたどったとき）．
func (m *Model) visit(n ast.Node, c *ctx) bool {
	if m.seen[n] {
		return false
	}
	m.seen[n] = true
	switch x := n.(type) {
	case *ast.ExportDeclaration:
		if x.IsDefault {
			c.pending = "default"
		}
	case *ast.Binding:
		if id, ok := x.Target.(*ast.Identifier); ok {
			if c.depth == 0 {
				c.pending = id.Name.String()
			}
			// 関数の中の const も，同じ名前の最上位の定数がなければ追う（const BASE_URL = __ENV.BASE_URL など）
			if _, dup := m.consts[id.Name.String()]; !dup && x.Initializer != nil {
				m.consts[id.Name.String()] = x.Initializer
			}
		}
	case *ast.FunctionLiteral, *ast.ArrowFunctionLiteral:
		if c.depth == 0 {
			name := c.pending
			if fl, ok := x.(*ast.FunctionLiteral); ok && fl.Name != nil && name != "default" {
				name = fl.Name.Name.String()
			}
			if name == "" {
				name = "（無名の関数）"
			}
			c.fn = name
			if fl, ok := x.(*ast.FunctionLiteral); ok {
				m.funcInfo(name, fl)
			}
		}
		c.depth++
		c.pending = ""
	case *ast.ClassLiteral:
		c.depth++
	case *ast.ForStatement, *ast.ForOfStatement, *ast.ForInStatement, *ast.WhileStatement, *ast.DoWhileStatement:
		c.loop = true
	case *ast.DotExpression:
		if id, ok := x.Left.(*ast.Identifier); ok {
			switch id.Name.String() {
			case "__ENV":
				m.EnvVars = appendUniq(m.EnvVars, x.Identifier.Name.String())
			case "process":
				m.ProcessUses = append(m.ProcessUses, m.line(x))
			}
		}
	case *ast.CallExpression:
		if g := m.call(x, c); g != "" {
			// group('名前', () => { ... }) の中のリクエストには group タグが付く
			inner := *c
			inner.group = g
			for _, a := range x.ArgumentList {
				m.walk(reflect.ValueOf(a), inner)
			}
			return false
		}
	}
	return true
}

func (m *Model) funcInfo(name string, fl *ast.FunctionLiteral) {
	if fl.ParameterList != nil && name == "default" {
		m.DefaultParams = len(fl.ParameterList.List)
	}
	if name == "setup" && fl.Body != nil {
		for _, st := range fl.Body.List {
			if r, ok := st.(*ast.ReturnStatement); ok && r.Argument != nil {
				m.SetupReturns = true
			}
		}
	}
}

func appendUniq(l []string, s string) []string {
	for _, x := range l {
		if x == s {
			return l
		}
	}
	return append(l, s)
}

var httpMethods = map[string]string{
	"get": "GET", "post": "POST", "put": "PUT", "patch": "PATCH", "del": "DELETE", "head": "HEAD", "options": "OPTIONS",
}

// call は関数の呼び出しを記録する．group() なら group の名前を返す．
func (m *Model) call(x *ast.CallExpression, c *ctx) string {
	args := x.ArgumentList
	arg := func(i int) ast.Expression {
		if i < len(args) {
			return args[i]
		}
		return nil
	}
	switch callee := x.Callee.(type) {
	case *ast.Identifier:
		name := callee.Name.String()
		mod := m.Imports[name]
		switch {
		case name == "open":
			m.Opens = append(m.Opens, Call{c.fn, m.line(x)})
		case name == "check" && mod == "k6":
			m.addCheck(x, c)
		case name == "sleep" && mod == "k6":
			m.Sleeps = append(m.Sleeps, Call{c.fn, m.line(x)})
		case name == "group" && mod == "k6":
			g, _ := m.eval(arg(0), 0).(string)
			m.Groups = appendUniq(m.Groups, g)
			return "::" + g
		case name == "require":
			m.Problems = append(m.Problems, Problem{"require", m.line(x), "require() は k6 では使えません．`import ... from '...'` で読み込みます"})
		}
	case *ast.DotExpression:
		obj, ok := callee.Left.(*ast.Identifier)
		if !ok || m.Imports[obj.Name.String()] != "k6/http" {
			return ""
		}
		method := callee.Identifier.Name.String()
		switch {
		case method == "request":
			mth, _ := m.eval(arg(0), 0).(string)
			m.addRequest(x, c, strings.ToUpper(mth), arg(1), arg(2), arg(3))
		case httpMethods[method] != "":
			if method == "get" || method == "head" {
				m.addRequest(x, c, httpMethods[method], arg(0), nil, arg(1))
			} else {
				m.addRequest(x, c, httpMethods[method], arg(0), arg(1), arg(2))
			}
		}
	}
	return ""
}

func (m *Model) addCheck(x *ast.CallExpression, c *ctx) {
	ch := Check{Func: c.fn, Line: m.line(x)}
	if len(x.ArgumentList) > 1 {
		if o := m.objectExpr(x.ArgumentList[1]); o != nil {
			for _, p := range o.Value {
				if pk, ok := p.(*ast.PropertyKeyed); ok {
					k, _ := m.propKey(pk)
					ch.Names = append(ch.Names, k)
					ch.Bodies = append(ch.Bodies, m.src(pk.Value))
				}
			}
		}
	}
	m.Checks = append(m.Checks, ch)
}

func (m *Model) addRequest(x *ast.CallExpression, c *ctx, method string, url, body, params ast.Expression) {
	r := Request{Method: method, Func: c.fn, InLoop: c.loop, Group: c.group, Line: m.line(x), URLSrc: m.src(url)}
	m.readURL(&r, url)
	if p := m.objectExpr(params); p != nil {
		r.Tags, r.TagsUnknown = m.stringMap(m.prop(p, "tags"))
		r.Headers, _ = m.stringMap(m.prop(p, "headers"))
	} else if params != nil {
		r.TagsUnknown = true
	}
	r.Body = m.bodyKind(body)
	m.Requests = append(m.Requests, r)
}

func (m *Model) bodyKind(e ast.Expression) string {
	if e == nil {
		return ""
	}
	if call, ok := e.(*ast.CallExpression); ok {
		if d, ok := call.Callee.(*ast.DotExpression); ok {
			if id, ok := d.Left.(*ast.Identifier); ok && id.Name.String() == "JSON" && d.Identifier.Name.String() == "stringify" {
				return "json"
			}
		}
	}
	if o := m.objectExpr(e); o != nil {
		for _, p := range o.Value {
			if pk, ok := p.(*ast.PropertyKeyed); ok && m.isHTTPFile(pk.Value) {
				return "file"
			}
		}
		return "object"
	}
	switch m.eval(e, 0).(type) {
	case string:
		return "string"
	case nil:
		return ""
	}
	return "other"
}

func (m *Model) isHTTPFile(e ast.Expression) bool {
	if id, ok := e.(*ast.Identifier); ok {
		e = m.consts[id.Name.String()]
	}
	call, ok := e.(*ast.CallExpression)
	if !ok {
		return false
	}
	d, ok := call.Callee.(*ast.DotExpression)
	if !ok {
		return false
	}
	id, ok := d.Left.(*ast.Identifier)
	return ok && m.Imports[id.Name.String()] == "k6/http" && d.Identifier.Name.String() == "file"
}

// urlPart は URL の式を分けた1片．
type urlPart struct {
	lit  string
	base bool // __ENV.BASE_URL（またはそれを入れた定数）
	dyn  bool // そのほかの決まらない値
}

func (m *Model) urlParts(e ast.Expression, depth int) []urlPart {
	if e == nil || depth > 8 {
		return []urlPart{{dyn: true}}
	}
	switch x := e.(type) {
	case *ast.StringLiteral:
		return []urlPart{{lit: x.Value.String()}}
	case *ast.TemplateLiteral:
		var out []urlPart
		for i, el := range x.Elements {
			out = append(out, urlPart{lit: el.Parsed.String()})
			if i < len(x.Expressions) {
				out = append(out, m.urlParts(x.Expressions[i], depth+1)...)
			}
		}
		return out
	case *ast.BinaryExpression:
		if x.Operator == token.PLUS {
			parts := append(m.urlParts(x.Left, depth+1), m.urlParts(x.Right, depth+1)...)
			for _, p := range parts {
				if p.lit != "" || p.base {
					return parts
				}
			}
			return []urlPart{{dyn: true}} // 数の足し算など，文字列をつなぐのではない +
		}
		if x.Operator == token.LOGICAL_OR {
			return m.urlParts(x.Left, depth+1)
		}
	case *ast.DotExpression:
		if id, ok := x.Left.(*ast.Identifier); ok && id.Name.String() == "__ENV" {
			if x.Identifier.Name.String() == "BASE_URL" {
				return []urlPart{{base: true}}
			}
		}
	case *ast.Identifier:
		if init, ok := m.consts[x.Name.String()]; ok {
			return m.urlParts(init, depth+1)
		}
	}
	return []urlPart{{dyn: true}}
}

var absURL = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+.-]*://([^/?#]*)(.*)$`)

func (m *Model) readURL(r *Request, e ast.Expression) {
	var parts []urlPart
	for _, p := range m.urlParts(e, 0) {
		if p.lit != "" || p.base || p.dyn {
			parts = append(parts, p) // テンプレート文字列の空の文字の部分を除く
		}
	}
	var b strings.Builder
	for i, p := range parts {
		switch {
		case p.base && i == 0:
			r.UsesBaseURL = true
		case p.base, p.dyn:
			b.WriteString("{}")
		default:
			b.WriteString(p.lit)
		}
	}
	s := b.String()
	for strings.Contains(s, "{}{}") {
		s = strings.ReplaceAll(s, "{}{}", "{}")
	}
	if !r.UsesBaseURL {
		if mm := absURL.FindStringSubmatch(s); mm != nil {
			r.Host, s = mm[1], mm[2]
		} else if strings.HasPrefix(s, "{}") {
			s = s[2:] // 決まらない接続先のあとにパスが続く
		}
	}
	if i := strings.IndexAny(s, "?#"); i >= 0 {
		r.Query = strings.TrimPrefix(s[i:], "?")
		s = s[:i]
	}
	if s == "" {
		s = "/"
	}
	r.Path = s
}

// readOptions は options を評価し，負荷のかけ方と閾値を取り出す．
func (m *Model) readOptions() {
	init, ok := m.consts["options"]
	if !ok {
		return
	}
	opt, ok := m.eval(init, 0).(map[string]any)
	if !ok {
		m.Problems = append(m.Problems, Problem{"options", m.line(init), "options をオブジェクトとして読めません"})
		return
	}
	m.Options = opt
	if b, err := json.Marshal(jsonable(opt)); err == nil {
		m.Exec, _ = runner.ParseInspect(b)
	}
	th, ok := opt["thresholds"]
	if !ok {
		return
	}
	thm, ok := th.(map[string]any)
	if !ok {
		m.Problems = append(m.Problems, Problem{"thresholds", 0, "thresholds はオブジェクト（キーが指標の名前）で書きます"})
		return
	}
	names := make([]string, 0, len(thm))
	for k := range thm {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, name := range names {
		t := Threshold{Name: name, Metric: runner.ParseMetricName(name)}
		list, ok := thm[name].([]any)
		if !ok {
			m.Problems = append(m.Problems, Problem{"threshold_not_array", 0,
				"閾値 `" + name + "` の値が配列ではありません．`['p(95)<500']` のように配列で書きます（k6 は終了コード 104 で止まります）"})
			if s, ok := thm[name].(string); ok {
				list = []any{s}
			}
		}
		for _, v := range list {
			var expr string
			switch x := v.(type) {
			case string:
				expr = x
			case map[string]any:
				expr, _ = x["threshold"].(string)
			}
			if expr == "" {
				continue
			}
			t.Exprs = append(t.Exprs, expr)
			if _, err := runner.ParseExpr(expr); err != nil {
				msg := "閾値 `" + name + "` の式 `" + expr + "` を k6 は読めません"
				if regexp.MustCompile(`\bp\d`).MatchString(expr) {
					msg += "．パーセンタイルは `p(95)` のように括弧で書きます"
				} else if regexp.MustCompile(`\d\s*(ms|s)\b`).MatchString(expr) {
					msg += "．数値に単位は付けません（時間はミリ秒）"
				}
				m.Problems = append(m.Problems, Problem{"threshold_expr", 0, msg})
			}
		}
		m.Thresholds = append(m.Thresholds, t)
	}
}

// jsonable は Unknown を JSON にできる形にする．
func jsonable(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := map[string]any{}
		for k, e := range x {
			out[k] = jsonable(e)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = jsonable(e)
		}
		return out
	case Unknown:
		return nil
	}
	return v
}

// findProblems は実行すると誤りになると分かることを集める．
func (m *Model) findProblems() {
	if m.HasOptions && !m.OptionsExported {
		m.Problems = append(m.Problems, Problem{"options_not_exported", 0, "options に export がありません．`export const options = { ... }` と書かないと，k6 は options を読みません"})
	}
	for _, o := range m.Opens {
		if o.Func != "" {
			m.Problems = append(m.Problems, Problem{"open_in_vu", o.Line, "open() が " + placeName(o.Func) + " の中にあります．open() は init 段階でだけ呼べるので，実行すると毎回 `only available in the init stage` のエラーになり，その回の処理は打ち切られます"})
		}
	}
	for _, l := range m.ProcessUses {
		m.Problems = append(m.Problems, Problem{"process", l, "`process` は Node.js のもので，k6 にはありません．実行すると `ReferenceError: process is not defined` になります．環境変数は `__ENV` で読みます"})
	}
	for _, r := range m.Requests {
		if r.Func == "" {
			m.Problems = append(m.Problems, Problem{"http_in_init", r.Line, "init 段階（関数の外）で HTTP リクエストを送っています．k6 では init 段階からリクエストを送れません．setup() か default 関数の中に書きます"})
		}
	}
	if sc, ok := m.Options["scenarios"].(map[string]any); ok {
		names := make([]string, 0, len(sc))
		for n := range sc {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			exec := "default"
			if cfg, ok := sc[n].(map[string]any); ok {
				if e, ok := cfg["exec"].(string); ok && e != "" {
					exec = e
				}
			}
			if cfg, ok := sc[n].(map[string]any); ok {
				if ex, _ := cfg["executor"].(string); strings.HasSuffix(ex, "arrival-rate") {
					if _, ok := cfg["preAllocatedVUs"]; !ok {
						m.Problems = append(m.Problems, Problem{"arrival_prealloc", 0, "シナリオ " + n + " は " + ex + " なので，preAllocatedVUs（あらかじめ用意する VU の数）が必要です"})
					}
				}
			}
			if !m.Exports[exec] {
				m.Problems = append(m.Problems, Problem{"exec_not_exported", 0, "シナリオ " + n + " の exec `" + exec + "` に当たる関数が export されていません（k6 は `function '" + exec + "' not found in exports` で止まります）"})
			}
		}
	} else if !m.Exports["default"] {
		m.Problems = append(m.Problems, Problem{"no_default", 0, "`export default function () { ... }` がありません．k6 が繰り返し実行する VU のコードを書きます"})
	}
	for _, t := range m.Thresholds {
		if len(t.Metric.Tags) > 0 && isHTTPMetric(t.Metric.Base) {
			if res := m.Measures(t); res.Certain && len(res.Requests) == 0 {
				m.Problems = append(m.Problems, Problem{"zero_sample", 0, "閾値 `" + t.Name + "` に当たるリクエストがありません．このままでは1件も測らずに「合格」します（測定0件）．リクエストに付けたタグと `{ }` の中を1文字ずつ比べよう"})
			}
		}
	}
}

func isHTTPMetric(base string) bool { return strings.HasPrefix(base, "http_req") }

// MeasureResult は閾値が測るリクエスト．Certain が false なら，静的には決めきれない．
type MeasureResult struct {
	Requests []int `json:"requests"` // Model.Requests の添字
	Certain  bool  `json:"certain"`
}

// Measures は閾値のタグの条件に合うリクエストを，コードから求める．
// k6 と同じく，name タグを付けると url タグも name の値になる．
func (m *Model) Measures(t Threshold) MeasureResult {
	res := MeasureResult{Certain: true}
	if !isHTTPMetric(t.Metric.Base) {
		res.Certain = false
		return res
	}
	globalTags, _ := m.Options["tags"].(map[string]any)
	for i, r := range m.Requests {
		if r.Func == "" {
			continue // init 段階のリクエストは送られない
		}
		ok, certain := m.requestMatches(r, t.Metric.Tags, globalTags)
		if !certain {
			res.Certain = false
		}
		if ok {
			res.Requests = append(res.Requests, i)
		}
	}
	return res
}

func (m *Model) requestMatches(r Request, want map[string]string, global map[string]any) (match, certain bool) {
	match, certain = true, true
	for k, v := range want {
		if (k == "name" || k == "url") && !hasKey(r.Tags, "name") && !r.TagsUnknown {
			// name タグがないと，name も url も URL そのものになる
			if urlMayEqual(v, r.Path) {
				certain = false
				match = false
				continue
			}
			return false, true
		}
		got, known := m.requestTag(r, k, global)
		switch {
		case !known:
			certain, match = false, false
		case got != v:
			return false, true
		}
	}
	return match, certain
}

func hasKey(m map[string]string, k string) bool {
	_, ok := m[k]
	return ok
}

// urlMayEqual は閾値に書いた URL の値が，パス（{} は決まらない部分）で終わりうるか．
func urlMayEqual(v, path string) bool {
	re := "^.*" + strings.ReplaceAll(regexp.QuoteMeta(path), `\{\}`, ".*") + "$"
	ok, _ := regexp.MatchString(re, v)
	return ok && strings.Contains(v, "/")
}

// requestTag は k6 がリクエストの計測値に付けるタグの値を，コードから推し量る．
func (m *Model) requestTag(r Request, key string, global map[string]any) (string, bool) {
	if v, ok := r.Tags[key]; ok {
		return v, !r.TagsUnknown || isLiteralTag(r, key)
	}
	switch key {
	case "name", "url":
		if n, ok := r.Tags["name"]; ok {
			return n, isLiteralTag(r, "name")
		}
		return "", false
	case "method":
		return r.Method, r.Method != ""
	case "group":
		return r.Group, true
	case "scenario":
		return m.scenarioOf(r)
	case "status", "expected_response", "error", "error_code", "proto", "tls_version":
		return "", false
	}
	if v, ok := global[key].(string); ok {
		return v, true
	}
	if r.TagsUnknown {
		return "", false
	}
	return "", true // その名前のタグは付かない
}

func isLiteralTag(r Request, key string) bool {
	v := r.Tags[key]
	return !strings.ContainsAny(v, "`$(")
}

func (m *Model) scenarioOf(r Request) (string, bool) {
	if r.Func == "setup" || r.Func == "teardown" {
		return "", true
	}
	if m.Exec == nil {
		return "default", r.Func == "default"
	}
	var names []string
	for _, s := range m.Exec.Scenarios {
		if s.Exec == r.Func {
			names = append(names, s.Name)
		}
	}
	if len(names) == 1 {
		return names[0], true
	}
	if len(names) == 0 && m.Exports[r.Func] {
		return "", true
	}
	return "", false // 関数の中から呼ばれる関数などは決めきれない
}

// TaggedThresholds はタグで絞った閾値があるか．
func (m *Model) TaggedThresholds() bool {
	for _, t := range m.Thresholds {
		if len(t.Metric.Tags) > 0 {
			return true
		}
	}
	return false
}

// HasProblem は code の問題が見つかったか．
func (m *Model) HasProblem(code string) bool {
	for _, p := range m.Problems {
		if p.Code == code {
			return true
		}
	}
	return false
}
