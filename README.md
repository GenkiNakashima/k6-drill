# k6ドリル

k6 を初めて使う人が，「こういう状況では k6 のコードをこう書く」を1〜2時間で身につけるための，ローカルで動く学習 Web アプリです．

- 各問題は **状況** から始まり，学習者は状況に合うコードを書きます．
- 「採点」は **静的解析** で行います．コードを実行せずに構文解析し，options，リクエスト，タグ，ヘッダー，check，閾値を読み取り，閾値が測るリクエストまで静的に求めて採点します．**k6 がなくても使えます**．
- 合格すると「この状況ではこう書く」（模範コード）と解説が出ます．採点で2回不合格になると，合格する前でも模範コードを見られます．合格したコードは「レシピ集」に「状況 → コード」として並びます．
- **拡張機能**：学習者の PC に k6 があれば，「k6 で実行」で練習用サーバーに実際に負荷をかけ，結果（閾値の実測値，測定件数，終了コード）の読み方を練習できます．結果は合否に数えません．

基本問題12問（約55分）と応用問題9問（うち4問は任意）があります．

## はじめかた（学生向け）

### 1. 用意するもの

- 配られた k6ドリルの実行ファイル（または，GitHub から取ってきたフォルダ一式）
- ブラウザ（Chrome，Edge，Safari，Firefox のどれか）

k6 は **入れなくても始められます**．採点は k6 を使いません．あとで「k6 で実行（拡張）」を試したくなったら，[6. k6 を入れる（任意）](#6-k6-を入れる任意) を見てください．

配られた実行ファイルを使うなら，ほかに入れるものはありません（Go も Node.js も npm も要りません）．インターネットへの接続も要りません．

実行ファイルが配られず，**フォルダ一式から起動する場合だけ** Go が必要です．[7. Go を入れる](#7-go-を入れるフォルダ一式から起動する場合だけ) を見てください．

### 2. 起動する

**配られた実行ファイルを使う場合**

お使いの PC に合うファイルを選びます．

| PC | ファイル |
| --- | --- |
| Mac（Apple シリコン：M1〜） | `k6-drill-mac-arm64` |
| Mac（Intel） | `k6-drill-mac-amd64` |
| Windows | `k6-drill-windows-amd64.exe` |
| Linux（Ubuntu など） | `k6-drill-linux-amd64` |

Mac がどちらかは，アップルメニュー →「この Mac について」の「チップ」で分かります（「Apple M…」なら Apple シリコン，「Intel」なら Intel）．

- **Mac・Linux**：ターミナルを開き，ファイルを置いたフォルダで次を実行します．

  ```sh
  cd ~/Downloads                 # ファイルを置いたフォルダ
  chmod +x k6-drill-mac-arm64    # 最初の1回だけ（実行できるようにする）
  ./k6-drill-mac-arm64
  ```

- **Windows**：`k6-drill-windows-amd64.exe` をダブルクリックします．黒いウィンドウ（コンソール）が開けば起動しています．

**フォルダ一式から起動する場合**（Go 1.26 以上が必要．入れ方は [7. Go を入れる](#7-go-を入れるフォルダ一式から起動する場合だけ)）

まず，GitHub からフォルダ一式を取ってきます．git を使う方法と，ZIP でダウンロードする方法があります．

- **git を使う**（git の入れ方は [8. git を入れる](#8-git-を入れる任意)）：ターミナル（Windows は PowerShell）で，フォルダを置きたい場所に移動してから実行します．

  ```sh
  cd ~/Documents     # フォルダを置きたい場所（どこでもよい）
  git clone https://github.com/GenkiNakashima/k6-drill.git
  cd k6-drill
  ```

- **ZIP でダウンロードする**（git がなくてもよい）：https://github.com/GenkiNakashima/k6-drill を開き，緑の「Code」ボタン →「Download ZIP」を押します．ダウンロードした ZIP を展開すると `k6-drill-main` というフォルダができるので，ターミナルでそのフォルダに移動します．

  ```sh
  cd ~/Downloads/k6-drill-main
  ```

フォルダに移動したら，起動します．

```sh
go run .
```

最初の1回だけ，Go が必要な部品（JavaScript の構文解析と YAML の読み込み）をインターネットから取ってくるので，少し時間がかかります．2回目からはオフラインでも起動できます．

**新しい版に更新する**：git で取ってきた場合は，`k6-drill` フォルダで `git pull` を実行してから，もう一度 `go run .` で起動します．ZIP の場合は，ZIP をダウンロードし直してください．どちらの場合も，進捗（ホームフォルダの `.k6-drill`）は消えません．

### 3. 使う

起動すると，ブラウザで http://localhost:8800/ が自動で開きます．開かないときは，このアドレスを自分でブラウザに入力してください．

1. 左の一覧から **B01** を選ぶ
2. 「状況」と「やること」を読み，エディタでコードを書く
3. **「採点」** を押す（Ctrl+Enter，Mac は ⌘+Enter でも可）
4. 不合格なら，「満たしていない条件」と「ヒント」を読んで直す．「コードから読み取ったこと」には，アプリがあなたのコードをどう読んだか（リクエスト，タグ，閾値が測るリクエスト）が出ます
5. 合格すると「この状況ではこう書く」と解説が出る．2回不合格になると，合格する前でも「模範コードを見る」が押せます

基本問題をすべて合格すると，応用問題が開きます．合格したコードはヘッダーの **「レシピ集」** にまとまるので，あとで k6 のスクリプトを書くときの参考にしてください．

書きかけのコードと合否は自動で保存されます（ホームフォルダの `.k6-drill`）．アプリを止めても，次に起動すれば続きから始められます．

### 4. 止める

起動したターミナル（Windows は黒いウィンドウ）で **Ctrl+C** を押します．Windows はウィンドウを閉じても止まります．ブラウザのタブを閉じるだけでは止まりません．

### 5. 困ったとき

| 症状 | どうするか |
| --- | --- |
| ブラウザが開かない | http://localhost:8800/ を自分で開く |
| 「画面のポート 127.0.0.1:8800 を使えません」と出て止まる | k6ドリルがもう起動していないか確かめる（別のターミナルやウィンドウ）．ほかのアプリが使っているなら `./k6-drill-mac-arm64 -port 8801` のように番号を変え，http://localhost:8801/ を開く |
| 「練習用サーバーのポート 127.0.0.1:8090 を使えません」と出て止まる | ほかのアプリが 8090 番を使っていないか確かめる．変えるなら `-target-port 8091` を付ける |
| Mac で「開発元を確認できないため開けません」と出る | ターミナルで `xattr -d com.apple.quarantine k6-drill-mac-arm64` を実行してから，もう一度起動する |
| Mac・Linux で `permission denied` と出る | `chmod +x k6-drill-mac-arm64` を実行してから，もう一度起動する |
| Windows で「Windows によって PC が保護されました」と出る | 「詳細情報」→「実行」を押す |
| 最初からやり直したい | 画面右上の「設定」→「すべての進捗を消す」 |
| 応用問題が開かない | 基本問題（B01〜B12）をすべて合格すると開きます |

### 6. k6 を入れる（任意）

k6 を入れると，各問題の「k6 で実行（拡張）」ボタンが出ます．押すと，あなたのコードで練習用サーバーに実際に負荷をかけ，k6 の結果の表示（閾値の実測値，測定件数，終了コード）を確かめられます．結果は合否に数えません．

| PC | 入れ方 |
| --- | --- |
| Mac | `brew install k6`（Homebrew が必要） |
| Windows | `winget install k6 --source winget` |
| Ubuntu | https://grafana.com/docs/k6/latest/set-up/install-k6/ の手順 |

入れたら，k6ドリルをいったん止めて起動し直してください．`k6 version` で `k6 v1.…` 以上が出れば使えます．

### 7. Go を入れる（フォルダ一式から起動する場合だけ）

配られた実行ファイルを使うなら，この手順は要りません．Go は **1.26 以上** が必要です．

**Mac**

次のどちらかで入れます．

- 公式のインストーラー（おすすめ）：https://go.dev/dl/ を開き，Apple シリコンなら `darwin-arm64.pkg`，Intel なら `darwin-amd64.pkg` をダウンロードして，ダブルクリックで入れる
- Homebrew を使っている場合：`brew install go`

**Windows**

次のどちらかで入れます．

- 公式のインストーラー（おすすめ）：https://go.dev/dl/ から `windows-amd64.msi` をダウンロードし，ダブルクリックで入れる
- PowerShell で：`winget install GoLang.Go`

**Ubuntu**

`sudo apt install golang` で入る Go は Ubuntu の版によっては古く（1.26 より前），k6ドリルを起動できません．次のどちらかで入れるのが確実です．

- snap を使う：

  ```sh
  sudo snap install go --classic
  ```

- 公式の配布物を使う：https://go.dev/dl/ で `linux-amd64.tar.gz` の最新の版の名前を確かめ，`go1.XX.X` の部分をその版に置きかえて実行する

  ```sh
  cd ~/Downloads
  sudo rm -rf /usr/local/go
  sudo tar -C /usr/local -xzf go1.XX.X.linux-amd64.tar.gz
  echo 'export PATH=$PATH:/usr/local/go/bin' >> ~/.profile
  source ~/.profile
  ```

**入ったかを確かめる**

ターミナル（Windows は PowerShell）を **新しく開き直して** から，次を実行します．

```sh
go version
```

`go version go1.26.0 …` のように，1.26 以上の版が出れば入っています．`command not found`（Windows は「認識されません」）と出るときは，ターミナルを開き直すか，PC を再起動してください．

### 8. git を入れる（任意）

git clone でフォルダを取ってくる場合だけ必要です．ZIP でダウンロードするなら要りません．すでに入っているかは，ターミナルで `git --version` を実行すると分かります（`git version 2.…` と出れば入っています）．

| PC | 入れ方 |
| --- | --- |
| Mac | ターミナルで `xcode-select --install` を実行し，出てきた画面で「インストール」を押す（Homebrew を使っているなら `brew install git` でもよい） |
| Windows | PowerShell で `winget install Git.Git`，または https://git-scm.com/downloads からインストーラーを入れる |
| Ubuntu | `sudo apt install git` |

入れたら，ターミナルを開き直してから `git --version` で確かめてください．

### フロントエンドについて

画面は HTML・CSS・素の JavaScript の3つのファイル（`web/`）だけで作られていて，Go の実行ファイルに埋め込まれています．**Node.js，npm，yarn などのパッケージマネージャーや，追加のライブラリは要りません**．ビルドの手順もありません．外部のサイト（CDN）からも何も読み込まないので，オフラインでも画面が表示されます．

---

以下は，アプリを開発する人・配る人向けの説明です．

## 必要なもの

- 実行ファイルをビルドする場合だけ Go 1.26 以上（JavaScript のパーサ `grafana/sobek` が要求するため）．入れ方は [7. Go を入れる](#7-go-を入れるフォルダ一式から起動する場合だけ)
- 拡張機能「k6 で実行」を使う場合だけ k6 v1 以上
  - Mac：`brew install k6`
  - Windows：`winget install k6 --source winget`
  - Ubuntu：https://grafana.com/docs/k6/latest/set-up/install-k6/ の手順
- フロントエンドに必要なものはありません（Node.js・npm・ライブラリ・ビルド工程なし．[フロントエンドについて](#フロントエンドについて)）

Go の依存（`gopkg.in/yaml.v3`，`github.com/grafana/sobek` など．`go.mod` を参照）は，最初の `go run .` か `go build` のときに自動でダウンロードされます．このときだけインターネットへの接続が必要です．ビルドした実行ファイルの利用には要りません．

## 起動

```sh
go run .            # 開発時
./k6-drill          # ビルドした実行ファイル
```

既定のブラウザで http://localhost:8800/ が開きます．練習用サーバー（拡張機能の負荷の相手）は http://localhost:8090 で動きます．どちらも 127.0.0.1 にだけ待ち受けます．

| フラグ | 既定値 | 内容 |
| --- | --- | --- |
| `-port` | 8800 | 画面のポート |
| `-target-port` | 8090 | 練習用サーバーのポート |
| `-k6` | PATH から探す | k6 の実行ファイル（拡張機能） |
| `-exercises` | 埋め込み | 問題ファイルのディレクトリ（`basic/`，`advanced/`，`assets/` を含む） |
| `-data` | `~/.k6-drill` | 進捗の保存先 |
| `-no-browser` | false | ブラウザを開かない |
| `-unlock` | false | 基本問題を終えなくても応用問題を開く（開発用） |

## 配布用のビルド

```sh
export CGO_ENABLED=0
GOOS=darwin  GOARCH=arm64 go build -trimpath -ldflags "-s -w" -o dist/k6-drill-mac-arm64 .
GOOS=darwin  GOARCH=amd64 go build -trimpath -ldflags "-s -w" -o dist/k6-drill-mac-amd64 .
GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "-s -w" -o dist/k6-drill-windows-amd64.exe .
GOOS=linux   GOARCH=amd64 go build -trimpath -ldflags "-s -w" -o dist/k6-drill-linux-amd64 .
(cd dist && shasum -a 256 k6-drill-* > SHA256SUMS.txt)
```

- `CGO_ENABLED=0`：C のライブラリに頼らない実行ファイルにする（Linux でも追加の部品なしで動く）
- `-trimpath`：ビルドした PC のフォルダの名前（ユーザー名を含む）を実行ファイルに残さない
- `-ldflags "-s -w"`：デバッグ情報を除いて小さくする（1つ約 8MB）
- `SHA256SUMS.txt`：配ったファイルが壊れていないかを確かめるためのハッシュ値（`shasum -a 256 -c SHA256SUMS.txt`）

## テスト

```sh
go test -short ./...                                   # 採点（静的解析）のテスト．k6 は要らない
go test ./internal/app -run Acceptance -v -timeout 30m # 拡張機能「k6 で実行」を本物の k6 で確かめる
```

`internal/judge` のテストが主な受け入れテストです．すべてのコードの問題で「初期コードは不合格，模範解答は合格」，よくある誤り（タグの大文字と小文字の違い，閾値を緩める，シナリオ名の綴り違いなど）を見抜くこと，別解が合格することを確かめます．

## 問題の足し方

`exercises/basic/` または `exercises/advanced/` に，ID と同じ名前の YAML ファイル（例：`B13.yaml`）を置きます．アプリのコードを変える必要はありません（埋め込みを使う場合は再ビルドが必要．`-exercises exercises` を付けて起動すれば再ビルドなしで試せます）．

| キー | 必須 | 内容 |
| --- | --- | --- |
| `id` | 必須 | ファイル名と一致させる |
| `level` | 必須 | `basic` か `advanced` |
| `title` / `minutes` | 必須 | 題名と目安時間（分） |
| `scenario` | コードの問題は推奨 | 「こういう状況では」の1行．レシピ集に出る |
| `optional` | 任意 | `true` なら任意問題 |
| `doc` | 基本問題は必須 | 参照ドキュメントの URL（文字列か配列） |
| `type` | 必須 | `code` か `quiz` |
| `statement` | 必須 | 問題文（Markdown）．`## 状況`，`## やること`，`## 書き方の型` の順を基本にする |
| `starter` / `solution` | `code` のとき必須 | 初期コードと模範コード |
| `assets` | 任意 | `exercises/assets/` から一緒に置くファイル（拡張機能で使う） |
| `quiz` | `quiz` のとき必須 | 設問の配列．`code` の問題に書くと，設問とコードの両方に合格して問題の合格になる（A05） |
| `judge.static` | `code` のとき必須 | 採点の条件（下の表） |
| `judge.run` | 任意 | 拡張機能「k6 で実行」の結果に対する期待 |
| `hints` | 任意 | 不合格の理由ごとのヒント |
| `explanation` | 必須 | 合格後に見せる解説 |

### judge.static に書ける条件

書いた条件をすべて満たしたとき合格です．加えて，**実行すると誤りになると分かるもの**（下の「自動で見つける誤り」）があれば，`allow_problems` に書かない限り不合格です．

| 条件 | 内容 |
| --- | --- |
| `imports` | import しているべきモジュール |
| `must_contain` / `must_not_contain` | コードを正規表現で照合する |
| `options` | options の値（例：`{vus: 5, duration: 10s}`．時間は表記の違いを無視） |
| `min_vus` / `max_vus` | VU の最大（stages の target，シナリオの VU を含む） |
| `min_stages` / `stages_end_at_zero` | stages の段数，最後の段の target が 0 か |
| `spike` | `{min_target, max_ramp}`：max_ramp 以内に min_target 以上へ（5倍以上に）一気に上げる段 |
| `min_scenarios` / `scenarios` | シナリオの数，`{name, exec, executor, fields}` に合うシナリオ |
| `requests` | `{method, path, path_regex, query, tags, headers（値は正規表現）, body（json/file/object）, in（default/setup/関数名）, uses_base_url, in_loop, min, max, label}` に合うリクエストの書かれた数．パスの決まらない部分は `{}`（例：`/api/items/{}`） |
| `thresholds` | `metric`（閾値の名前）か `for_path`（タグで絞った http_req_duration の閾値のうち，**測るリクエストがすべてそのパス** のもの．タグの名前は学習者に任せられる）で選び，`expr`，`max_ms` を確かめる |
| `thresholds_unchanged` | 閾値が初期コードと同じか |
| `checks` | `{min, min_names, status: [200, 404], uses_json, in}` |
| `open_in_init` | `open()` があり，関数の外にあるか |
| `env` | `__ENV.X` で読んでいる変数 |
| `setup_returns` / `default_uses_data` | setup() が値を返し，default 関数が引数で受け取るか |
| `sleep` | sleep() があるか |

### 自動で見つける誤り（problem）

| code | 内容 |
| --- | --- |
| `zero_sample` | タグで絞った閾値に当たるリクエストがない（測定0件のまま合格する） |
| `open_in_vu` | `open()` が関数の中にある |
| `process` | Node.js の `process` を使っている |
| `http_in_init` | init 段階で HTTP リクエストを送っている |
| `options_not_exported` | `options` に export がない |
| `threshold_not_array` / `threshold_expr` | 閾値の値が配列でない，式を k6 が読めない（`p95<300`，`300ms` など） |
| `exec_not_exported` / `no_default` | シナリオの exec の関数，default 関数が export されていない |
| `arrival_prealloc` | arrival-rate の executor に preAllocatedVUs がない |
| `require` | require() を使っている |

閾値が測るリクエストは，k6 と同じ規則で静的に求めます：閾値の `{ }` のタグがリクエストのタグと完全に一致するもの．`name` タグがないリクエストの `name` と `url` は URL そのもの，`scenario` はそのリクエストを書いた関数を `exec` にするシナリオ，`method` は HTTP のメソッドです．

### hints の when

| when | 当てはまるとき |
| --- | --- |
| `zero_sample_threshold` | 測定0件の閾値がある（採点では静的に，実機実行では計測値から） |
| `only_aggregate_threshold` | タグで絞った閾値がない |
| `{fail: requests}` | その条件（失敗の code）を満たさなかった |
| `{problem: open_in_vu}` | その誤りが見つかった |
| `{exit_code: 99}` / `{log_contains: 文字列}` / `{status: 401}` | 実機実行の結果について |
| `always` | 不合格なら常に |

## 構成

```text
main.go              起動：画面と練習用サーバーを立てる
internal/analyze/    k6 スクリプトの静的解析（sobek の構文木から options・リクエスト・閾値などを読む）
internal/judge/      採点（static.go：静的解析が主），実機実行の判定（judge.go：拡張機能）
internal/exercise/   問題ファイルの読み込みと検査
internal/progress/   進捗（~/.k6-drill/progress.json）と任意の記録（record.jsonl）
internal/app/        画面が呼ぶ API
internal/target/     練習用サーバー（:8090）．遅延は待機と同時実行数の制限だけで作る
internal/runner/     拡張機能：k6 inspect と k6 run の実行，集計表示と計測値の集計
exercises/           問題ファイルと assets
web/                 画面（HTML・CSS・素の JavaScript．ビルド工程なし）
```

## 拡張機能「k6 で実行」の仕組み（要点）

1. コードを一時ディレクトリに `script.js` として書き，`assets` を同じ場所に置く
2. `k6 inspect --execution-requirements` で options を解決する．VU 100 超か実行時間 60 秒超なら実行を断る
3. 練習用サーバーのカウンタと順番待ちを初期化する
4. `k6 run -e BASE_URL=http://localhost:8090 --out json=points.json drill-entry.js` を 120 秒の上限で実行する．`drill-entry.js` は `script.js` をそのまま読み込む入口で，各リクエストに URL のパスを `drill_path` タグとして足すだけである（k6 は `name` タグを付けたリクエストの `url` タグを name の値で上書きするため）
5. 閾値の合否と実測値は標準出力の THRESHOLDS 欄から読み，測定件数は `points.json` から数える
6. `handleSummary` は使わない（通常の集計表示を読む問題があるため）
