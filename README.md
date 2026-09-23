# slack-commander-opencode-session

`slack-commander` から OpenCode を利用するための小さなコマンドです。

Slack のスレッドごとに OpenCode のセッションを対応付け、同じスレッドから続けて呼び出したときは以前のセッションを再利用します。

OpenCode は `opencode serve` で常駐させ、このコマンドから HTTP API を呼び出します。OpenCode CLI を毎回起動する方式ではありません。

## 仕組み

Slack のチャンネル ID とスレッドのタイムスタンプから、次の形式のセッションタイトルを作ります。

```text
slack:<SLACK_CHANNEL_ID>:<SLACK_THREAD_TS>
```

例えば、

```text
SLACK_CHANNEL_ID=C01234567
SLACK_THREAD_TS=1780000123.456789
```

なら、セッションタイトルは次のようになります。

```text
slack:C01234567:1780000123.456789
```

実行時は、まずこのタイトルに対応する OpenCode のセッションを検索します。

```text
GET /session?search=<title>
```

検索結果はタイトルの完全一致でも確認します。

* 一致するセッションがない場合は、新しいセッションを作成する
* 1件だけ見つかった場合は、そのセッションを再利用する
* 2件以上見つかった場合は、どれを使うか判断せずエラー終了する

新しいセッションを作成した場合も、既存のセッションを見つけた場合も、そのセッションへ次の API で入力を送ります。

```text
POST /session/<session-id>/message
```

OpenCode から返されたレスポンスのうち、`text` part だけを標準出力へ出力します。reasoning や tool call は出力しません。

このコマンド自身は、Slack スレッドと OpenCode セッションの対応表を保存しません。OpenCode に保存されたセッションタイトルだけを使って、対応するセッションを探します。

## 使い方

```bash
opencode-session run "今日のtodoを教えて"
```

`run` より後ろに複数の引数を指定した場合は、スペースでつないで1つの入力として送信します。

例えば、

```bash
opencode-session run 今日の todo を教えて
```

は、次の入力として扱われます。

```text
今日の todo を教えて
```

OpenCode CLI のオプションをそのまま渡す機能はありません。

## 環境変数

`SLACK_CHANNEL_ID` と `SLACK_THREAD_TS` は必須です。

| 変数                 | 説明                                                  |
| ------------------ | --------------------------------------------------- |
| `SLACK_CHANNEL_ID` | Slack のチャンネル ID                                     |
| `SLACK_THREAD_TS`  | Slack のスレッドを識別するタイムスタンプ                             |
| `OPENCODE_URL`     | `opencode serve` の URL。未設定時は `http://opencode:4096` |

OpenCode へのリクエストでは、作業ディレクトリとして `/workspace` を指定します。

実際には、次の HTTP ヘッダーが付加されます。

```text
x-opencode-directory: %2Fworkspace
```

## OpenCode 側の準備

あらかじめ `opencode serve` を常駐させておく必要があります。

OpenCode の認証情報や MCP サーバなどの設定は、`opencode serve` を実行する側で行ってください。このコマンドは、それらの設定や状態を管理しません。

例えば Docker Compose で利用する場合は、`opencode` を通常の常駐サービスとして起動し、このコマンドから

```text
http://opencode:4096
```

へ接続できるようにします。

## 同時実行について

同じ Slack スレッドに対する実行は、呼び出し側で直列化する必要があります。

このコマンド自身は排他制御を行いません。

同じスレッドに対して初回の呼び出しが同時に実行されると、どちらも「対応するセッションがない」と判断し、同じタイトルのセッションを複数作成する可能性があります。

`slack-commander` から利用する場合は、同じスレッドの処理が同時に実行されないようにしてください。

## エラー時の動作

次のような場合はエラー終了します。

* `SLACK_CHANNEL_ID` または `SLACK_THREAD_TS` が設定されていない
* `opencode serve` に接続できない
* OpenCode API がエラーを返した
* OpenCode API のレスポンスを正しく読み取れない
* 同じタイトルのセッションが複数存在する
* 一致したセッションに ID がない
* OpenCode の回答に `text` part が含まれていない

セッションの検索に失敗した場合、それを「セッションが存在しない」とみなして新規作成することはありません。

## 開発

```bash
go test ./...
go vet ./...
golangci-lint run
```

テストでは `httptest.Server` を使って OpenCode API の動作を再現するため、実際の `opencode serve` は必要ありません。

## コンテナイメージ

コンテナイメージは `ko` でビルドします。

`v*` のタグを push すると GitHub Actions が `linux/amd64` と `linux/arm64` のイメージをビルドし、GHCR に公開します。

詳細は `.ko.yaml` と `.github/workflows/ci.yml` を参照してください。
