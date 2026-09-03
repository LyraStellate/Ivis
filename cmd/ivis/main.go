// Command ivis はローカル Web サーバーとして起動し、ブラウザから使う。
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"time"

	"github.com/LyraStellate/Ivis/internal/agent"
	"github.com/LyraStellate/Ivis/internal/config"
	"github.com/LyraStellate/Ivis/internal/httpapi"
	"github.com/LyraStellate/Ivis/internal/provider"
	"github.com/LyraStellate/Ivis/internal/provider/ollama"
	"github.com/LyraStellate/Ivis/internal/skillreg"
	"github.com/LyraStellate/Ivis/internal/store"
	"github.com/LyraStellate/Ivis/internal/tools"
	"github.com/LyraStellate/Ivis/web"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "ivis: "+err.Error())
		os.Exit(1)
	}
}

func run() error {
	cfgPath := flag.String("config", "", "設定ファイルの位置 (既定: ~/.ivis/config.json)")
	listen := flag.String("listen", "", "待ち受けアドレス (設定より優先)")
	flag.Parse()

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}
	if *listen != "" {
		cfg.Listen = *listen
	}
	if err := cfg.EnsureDirs(); err != nil {
		return err
	}
	// 定義が 1 つも無いと何も起動できず、入口 (規定エージェント) が失われると
	// どのエージェントも呼べない。足りない分だけ補う。
	if err := agent.Bootstrap(cfg.AgentPaths); err != nil {
		log.Printf("エージェントの雛形を書けませんでした: %v", err)
	}

	st, err := store.Open(cfg.DBPath())
	if err != nil {
		return err
	}
	defer st.Close()

	agents := agent.NewSet()
	agents.Load(cfg.AgentPaths)
	skills := skillreg.New()
	skills.Load(cfg.SkillPaths)

	newProvider := func(baseURL string) provider.Provider { return ollama.New(baseURL) }

	srv := httpapi.New(httpapi.Deps{
		Config:      cfg,
		Store:       st,
		Agents:      agents,
		Skills:      skills,
		Tools:       tools.NewRegistry(),
		Prov:        newProvider(cfg.OllamaBaseURL),
		NewProvider: newProvider,
		Assets:      web.Assets(),
	})

	ln, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		return fmt.Errorf("待ち受けを開始できませんでした (%s): %w", cfg.Listen, err)
	}

	report(cfg, agents, skills, ln.Addr().String())

	httpSrv := &http.Server{Handler: srv.Handler()}
	errCh := make(chan error, 1)
	go func() {
		if err := httpSrv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return httpSrv.Shutdown(shutdownCtx)
}

// printAddresses は開くべき URL を出す。
//
// ワイルドカードへ束ねたときは、そのまま出しても開けない。実際に届く宛先を
// 並べ、tailnet のものには印を付ける。VPN 越しに使いたいのに、どのアドレスを
// 叩けばよいか分からない、という状態を作らないため。
func printAddresses(addr string) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		fmt.Printf("Ivis  http://%s\n", addr)
		return
	}
	ip := net.ParseIP(host)
	if ip != nil && !ip.IsUnspecified() {
		fmt.Printf("Ivis  http://%s\n", net.JoinHostPort(host, port))
		if !ip.IsLoopback() {
			warnOpen()
		}
		return
	}

	fmt.Printf("Ivis  http://%s\n", net.JoinHostPort("127.0.0.1", port))
	for _, a := range localAddresses() {
		note := ""
		if isTailnet(a) {
			note = "  (Tailscale)"
		}
		fmt.Printf("      http://%s%s\n", net.JoinHostPort(a.String(), port), note)
	}
	warnOpen()
}

// warnOpen はループバック以外へ開いたことを伝える。Ivis は誰が繋いできたかを
// 問わないので、到達できる範囲を絞るのは利用者側の仕事になる。
func warnOpen() {
	fmt.Println("  ! ループバック以外へ開いています。認証は無く、ファイルの読み書きと")
	fmt.Println("    スクリプトの実行が届く相手すべてに使えます。到達範囲を絞ってください。")
}

// localAddresses は稼働中のインターフェースが持つ IP を返す。
func localAddresses() []net.IP {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var out []net.IP
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			n, ok := a.(*net.IPNet)
			if !ok || n.IP.IsLinkLocalUnicast() {
				continue
			}
			// v6 は表示が長く、ここでの目的 (開く先を知る) には要らない。
			if v4 := n.IP.To4(); v4 != nil {
				out = append(out, v4)
			}
		}
	}
	return out
}

// isTailnet は Tailscale が配る範囲 (100.64.0.0/10) かを返す。
func isTailnet(ip net.IP) bool {
	v4 := ip.To4()
	return v4 != nil && v4[0] == 100 && v4[1] >= 64 && v4[1] <= 127
}

// report は起動時に、読み込み結果と問題点を標準出力に出す。黙って起動すると
// 「編集したスキルが反映されない」ような状況で手がかりが残らない。
func report(cfg *config.Config, agents *agent.Set, skills *skillreg.Registry, addr string) {
	printAddresses(addr)
	fmt.Printf("  設定          %s\n", cfg.Path())
	fmt.Printf("  Ollama        %s\n", cfg.OllamaBaseURL)
	fmt.Printf("  作業ディレクトリ %s (会話ごとに %s/<ID> へ分かれる)\n",
		cfg.WorkspaceDir, config.SeriesDir)
	fmt.Printf("  エージェント    %d 件\n", len(agents.List()))
	fmt.Printf("  スキル          %d 件\n", len(skills.List()))

	for _, e := range agents.Errors() {
		fmt.Printf("  ! エージェント %s: %s\n", e.Path, e.Reason)
	}
	for _, e := range skills.Errors() {
		fmt.Printf("  ! スキル %s: %s\n", e.Path, e.Reason)
	}
	for _, c := range skills.Conflicts() {
		fmt.Printf("  ! スキル名 %s が重複: %s を使い、%s は無視します\n", c.Name, c.Winner, c.Shadows)
	}
}
