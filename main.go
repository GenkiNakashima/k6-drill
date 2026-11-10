// k6ドリルは，k6 の書き方と結果の読み方を身につけるための，ローカルで動く演習アプリである．
//
// 画面（:8800）と練習用サーバー（:8090）を立て，学習者の PC にある k6 を起動して判定する．
package main

import (
	"context"
	"embed"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"time"

	"k6-drill/internal/app"
	"k6-drill/internal/exercise"
	"k6-drill/internal/progress"
	"k6-drill/internal/runner"
	"k6-drill/internal/target"
)

//go:embed exercises web
var embedded embed.FS

func main() {
	port := flag.Int("port", 8800, "画面の待ち受けポート")
	targetPort := flag.Int("target-port", 8090, "練習用サーバーの待ち受けポート")
	k6Path := flag.String("k6", "", "k6 の実行ファイル（省略時は PATH から探す）")
	exDir := flag.String("exercises", "", "問題ファイルのディレクトリ（省略時は実行ファイルに埋め込んだもの）")
	dataDir := flag.String("data", "", "進捗の保存先（省略時は ~/.k6-drill）")
	noBrowser := flag.Bool("no-browser", false, "起動時にブラウザを開かない")
	unlock := flag.Bool("unlock", false, "基本問題を終えなくても応用問題を開く（開発用）")
	flag.Parse()

	exFS, err := fs.Sub(embedded, "exercises")
	if err != nil {
		log.Fatal(err)
	}
	if *exDir != "" {
		exFS = os.DirFS(*exDir)
	}
	set, err := exercise.Load(exFS)
	if err != nil {
		log.Fatalf("問題ファイルを読めません: %v", err)
	}
	webFS, _ := fs.Sub(embedded, "web")

	if *dataDir == "" {
		if *dataDir, err = progress.DefaultDir(); err != nil {
			log.Fatal(err)
		}
	}
	store, err := progress.Open(*dataDir)
	if err != nil {
		log.Fatalf("進捗を読めません: %v", err)
	}

	k6 := *k6Path
	if k6 == "" {
		if p, err := exec.LookPath("k6"); err == nil {
			k6 = p
		} else {
			k6 = "k6"
			log.Print("k6 が見つかりません．画面に入れ方を表示します")
		}
	}

	tgt := target.New()
	targetAddr := fmt.Sprintf("127.0.0.1:%d", *targetPort)
	targetURL := fmt.Sprintf("http://localhost:%d", *targetPort)
	a := &app.App{
		Set:       set,
		Store:     store,
		Web:       webFS,
		TargetURL: targetURL,
		Unlock:    *unlock,
		Runner: &runner.Runner{
			K6:      k6,
			BaseURL: targetURL,
			Assets:  exFS,
			Reset:   tgt.Reset,
		},
	}

	targetSrv := &http.Server{Handler: tgt.Handler(), ReadHeaderTimeout: 10 * time.Second}
	appSrv := &http.Server{Handler: a.Handler(), ReadHeaderTimeout: 10 * time.Second}
	appAddr := fmt.Sprintf("127.0.0.1:%d", *port)

	tl, err := net.Listen("tcp", targetAddr)
	if err != nil {
		log.Fatalf("練習用サーバーのポート %s を使えません（ほかのプログラムが使っていないか確かめてください）: %v", targetAddr, err)
	}
	al, err := net.Listen("tcp", appAddr)
	if err != nil {
		log.Fatalf("画面のポート %s を使えません（k6ドリルがすでに起動していないか確かめてください）: %v", appAddr, err)
	}
	go serve(targetSrv, tl)
	go serve(appSrv, al)

	url := fmt.Sprintf("http://localhost:%d/", *port)
	log.Printf("k6ドリルを起動しました: %s （練習用サーバー %s，問題 %d 問，進捗 %s）", url, targetURL, len(set.List), store.Dir())
	log.Print("終了するには Ctrl+C を押してください")
	if !*noBrowser {
		if err := openBrowser(url); err != nil {
			log.Printf("ブラウザを開けませんでした．%s を開いてください", url)
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	<-ctx.Done()
	sctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = appSrv.Shutdown(sctx)
	_ = targetSrv.Shutdown(sctx)
}

func serve(s *http.Server, l net.Listener) {
	if err := s.Serve(l); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

// openBrowser は既定のブラウザで画面を開く．
func openBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", "", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	return cmd.Start()
}
