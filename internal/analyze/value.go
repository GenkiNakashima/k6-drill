package analyze

import (
	"strconv"

	"github.com/grafana/sobek/ast"
	"github.com/grafana/sobek/token"
)

// Unknown は静的には値が決まらない式．Src は元のコード．
type Unknown struct{ Src string }

// eval は式を静的に評価する．リテラル，オブジェクト，配列，式を含まないテンプレート文字列，
// 最上位の const が指すリテラルまでを追い，それ以外は Unknown を返す．
func (m *Model) eval(e ast.Expression, depth int) any {
	if depth > 8 || e == nil {
		return Unknown{m.src(e)}
	}
	switch x := e.(type) {
	case *ast.StringLiteral:
		return x.Value.String()
	case *ast.NumberLiteral:
		switch v := x.Value.(type) {
		case int64:
			return float64(v)
		case float64:
			return v
		}
	case *ast.BooleanLiteral:
		return x.Value
	case *ast.NullLiteral:
		return nil
	case *ast.TemplateLiteral:
		if x.Tag == nil && len(x.Expressions) == 0 && len(x.Elements) == 1 {
			return x.Elements[0].Parsed.String()
		}
	case *ast.UnaryExpression:
		if x.Operator == token.MINUS {
			if f, ok := m.eval(x.Operand, depth+1).(float64); ok {
				return -f
			}
		}
	case *ast.ArrayLiteral:
		out := make([]any, 0, len(x.Value))
		for _, v := range x.Value {
			out = append(out, m.eval(v, depth+1))
		}
		return out
	case *ast.ObjectLiteral:
		out := map[string]any{}
		for _, p := range x.Value {
			switch pp := p.(type) {
			case *ast.PropertyKeyed:
				if k, ok := m.propKey(pp); ok {
					out[k] = m.eval(pp.Value, depth+1)
				}
			case *ast.PropertyShort:
				out[pp.Name.Name.String()] = m.eval(&pp.Name, depth+1)
			}
		}
		return out
	case *ast.Identifier:
		if init, ok := m.consts[x.Name.String()]; ok {
			return m.eval(init, depth+1)
		}
	}
	return Unknown{m.src(e)}
}

func (m *Model) propKey(p *ast.PropertyKeyed) (string, bool) {
	if p.Computed {
		s, ok := m.eval(p.Key, 0).(string)
		return s, ok
	}
	switch k := p.Key.(type) {
	case *ast.StringLiteral:
		return k.Value.String(), true
	case *ast.Identifier:
		return k.Name.String(), true
	case *ast.NumberLiteral:
		return k.Literal, true
	}
	return "", false
}

// objectExpr は式がオブジェクトリテラル（または const で指したもの）なら，それを返す．
func (m *Model) objectExpr(e ast.Expression) *ast.ObjectLiteral {
	for i := 0; i < 8 && e != nil; i++ {
		switch x := e.(type) {
		case *ast.ObjectLiteral:
			return x
		case *ast.Identifier:
			e = m.consts[x.Name.String()]
			continue
		}
		return nil
	}
	return nil
}

// prop はオブジェクトリテラルの中のキーの値の式を返す．
func (m *Model) prop(o *ast.ObjectLiteral, key string) ast.Expression {
	if o == nil {
		return nil
	}
	for _, p := range o.Value {
		switch pp := p.(type) {
		case *ast.PropertyKeyed:
			if k, ok := m.propKey(pp); ok && k == key {
				return pp.Value
			}
		case *ast.PropertyShort:
			if pp.Name.Name.String() == key {
				return &pp.Name
			}
		}
	}
	return nil
}

// stringMap は {k: 'v'} の形のオブジェクトを読む．値が決まらないものは元のコードを入れ，unknown を true にする．
func (m *Model) stringMap(e ast.Expression) (out map[string]string, unknown bool) {
	o := m.objectExpr(e)
	if o == nil {
		return nil, e != nil
	}
	out = map[string]string{}
	for _, p := range o.Value {
		var k string
		var v ast.Expression
		switch pp := p.(type) {
		case *ast.PropertyKeyed:
			kk, ok := m.propKey(pp)
			if !ok {
				unknown = true
				continue
			}
			k, v = kk, pp.Value
		case *ast.PropertyShort:
			k, v = pp.Name.Name.String(), &pp.Name
		default:
			unknown = true
			continue
		}
		switch val := m.eval(v, 0).(type) {
		case string:
			out[k] = val
		case float64:
			out[k] = strconv.FormatFloat(val, 'f', -1, 64)
		case bool:
			out[k] = strconv.FormatBool(val)
		default:
			out[k] = m.render(v)
			unknown = true
		}
	}
	return out, unknown
}

// render は文字列を組み立てる式を，決まらない部分を {} にした文字列にする（`Bearer ${token}` → `Bearer {}`）．
func (m *Model) render(e ast.Expression) string {
	var b []byte
	for _, p := range m.urlParts(e, 0) {
		if p.base || p.dyn {
			b = append(b, "{}"...)
		} else {
			b = append(b, p.lit...)
		}
	}
	return string(b)
}
