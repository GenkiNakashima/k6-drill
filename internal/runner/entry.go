package runner

import (
	"os"
	"path/filepath"

	"github.com/grafana/sobek/ast"
	"github.com/grafana/sobek/parser"
)

// entryFile は k6 run に渡す入口のファイル．学習者のコード（script.js）をそのまま読み込む．
const entryFile = "drill-entry.js"

// PathTag は入口のファイルがリクエストに付けるタグ．URL のパスを入れる．
//
// k6 はリクエストに name タグを付けると url タグを name の値で上書きするので，
// 計測値からは本当の URL が分からなくなる．「/api/heavy だけを測る閾値」を見分けるために，
// http の関数を包んでパスを別のタグに残す．学習者のタグや閾値の照合には影響しない．
const PathTag = "drill_path"

const entryHead = `// k6ドリルの実行器が作る入口のファイル．学習者のコードは script.js にある．
import http from 'k6/http';

function drillPath(u) {
  const s = String(u && u.url !== undefined ? u.url : u);
  const m = s.match(/^[a-z][a-z0-9+.-]*:\/\/[^/?#]*([^?#]*)/i);
  return m ? (m[1] || '/') : s;
}
function drillParams(url, params) {
  const p = Object.assign({}, params || {});
  p.tags = Object.assign({}, p.tags || {}, { ` + PathTag + `: drillPath(url) });
  return p;
}
for (const m of ['get', 'head']) {
  const orig = http[m];
  http[m] = (url, params) => orig(url, drillParams(url, params));
}
for (const m of ['post', 'put', 'patch', 'del', 'options']) {
  const orig = http[m];
  http[m] = (url, body, params) => orig(url, body, drillParams(url, params));
}
{
  const orig = http.request;
  http.request = (method, url, body, params) => orig(method, url, body, drillParams(url, params));
}

export * from './script.js';
`

// writeEntry は入口のファイルを書き，そのファイル名を返す．
// 構文解析できないコードは入口を使わずに script.js を直接渡し，k6 のエラー文がそのまま出るようにする．
func writeEntry(dir, code string) string {
	hasDefault, err := hasDefaultExport(code)
	if err != nil {
		return "script.js"
	}
	src := entryHead
	if hasDefault {
		src += "export { default } from './script.js';\n"
	}
	if os.WriteFile(filepath.Join(dir, entryFile), []byte(src), 0o600) != nil {
		return "script.js"
	}
	return entryFile
}

func hasDefaultExport(code string) (bool, error) {
	prog, err := parser.ParseFile(nil, "script.js", code, 0, parser.IsModule, parser.WithDisableSourceMaps)
	if err != nil {
		return false, err
	}
	for _, st := range prog.Body {
		ex, ok := st.(*ast.ExportDeclaration)
		if !ok {
			continue
		}
		if ex.IsDefault {
			return true, nil
		}
		if ex.NamedExports != nil {
			for _, sp := range ex.NamedExports.ExportsList {
				if sp.Alias == "default" || (sp.Alias == "" && sp.IdentifierName == "default") {
					return true, nil
				}
			}
		}
	}
	return false, nil
}
