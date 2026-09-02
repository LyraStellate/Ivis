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
	// 定義が 1 つも無いと何も起動できないため、初回だけ雛形を置く。
	if len(cfg.AgentPaths) > 0 {
		if err := agent.WriteStarter(cfg.AgentPaths[0]); err != nil {
			log.Printf("エージェントの雛形を書けませんでした: %v", err)
		}
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

// report は起動時に、読み込み結果と問題点を標準出力に出す。黙って起動すると
// 「編集したスキルが反映されない」ような状況で手がかりが残らない。
func report(cfg *config.Config, agents *agent.Set, skills *skillreg.Registry, addr string) {
	fmt.Printf("Ivis  http://%s\n", addr)
	fmt.Printf("  設定          %s\n", cfg.Path())
	fmt.Printf("  Ollama        %s\n", cfg.OllamaBaseURL)
	fmt.Printf("  作業ディレクトリ %s\n", cfg.WorkspaceDir)
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
