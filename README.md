# Ivis

Ollama に接続して使う、軽量な AI チャット / エージェントフレームワーク。

単一バイナリを起動してブラウザで開く。エージェントは JSON、スキルは Claude 形式の
`SKILL.md` で管理し、既存の Claude スキル置き場をそのまま探索パスに指定できる。

設計の経緯と決定は [docs/design/](docs/design/) にある。

## ビルド

フロントエンドをビルドしてから Go をビルドする。成果物はバイナリへ埋め込まれる。

```
cd web && npm install && npm run build && cd ..
go build -o ivis ./cmd/ivis
```

## 起動

```
./ivis
```

既定で `http://127.0.0.1:8317` を待ち受ける。初回起動時に `~/.ivis/config.json` と
エージェントの雛形を書き出す。以降、設定・エージェント定義・スキルはファイルが正で、
Ivis 側から書き戻すことはない。

別マシンの Ollama を使う場合は設定画面か `~/.ivis/config.json` の
`ollama_base_url` を変える。

## 開発

フロントエンドを触る間は Vite の dev server を使う。`/api` は Go 側へ中継される。

```
./ivis            # 別の端末で
cd web && npm run dev
```

テストは `go test ./...`。

## 現状

v1 の基盤まで。Ollama 以外のプロバイダ、任意のシェル実行、認証、MCP 連携は
意図的に範囲外にしてある。理由は設計書の非ゴールを参照。
