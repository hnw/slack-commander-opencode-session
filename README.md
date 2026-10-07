# slack-commander-opencode-session

`slack-commander` から OpenCode を利用するための小さなコマンドです。

Slack のスレッドごとに OpenCode のセッションを対応付け、同じスレッドから続けて呼び出したときは以前のセッションを再利用します。

OpenCode は V2 の `opencode serve` で常駐させ、このコマンドから HTTP API を呼び出します。OpenCode CLI を毎回起動する方式ではありません。

このコマンド自身は状態を一切保持しません。Slack のスレッドとセッションの対応表も、セッション ID も保存しません。実行のたびに OpenCode のセッションタイトルから対応するセッションを探し直します。

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

実行は次の順番です。

```text
OpenCode V2 serve を常駐
↓
Slack thread からセッションを解決
↓
prompt を submit
↓
run の完了を poll
↓
assistant message を取得
```

### 1. セッションの解決

まずこのタイトルに完全一致するセッションを検索します。

```text
GET /api/session?search=<title>&directory=/workspace
```

検索はサーバー側で曖昧に行われるため、返ってきたセッションのうち実際にタイトルが完全一致するものだけをクライアント側で判定します。

セッション一覧はページングされるため、`cursor.next` が返る間は次のページも取得し、全ページした結果に対して完全一致を判定します。これにより、exact match が 2 ページ目にある場合や、同じタイトルのセッションがページをまたぐ場合も、漏れなく検出できます。cursor は解釈せず、そのままサーバーに返すだけの opaque な値として扱います。

* 一致するセッションがない場合は、新しいセッションを作成する
* 1件だけ見つかった場合は、そのセッションを再利用する
* 2件以上見つかった場合は、どれを使うか判断せずエラー終了する

セッションの作成は次の API で行います。

```text
POST /api/session
```

```json
{
  "title": "slack:C01234567:1780000123.456789",
  "location": { "directory": "/workspace" }
}
```

### 2. prompt の送信

セッションには次の API で入力を送ります。

```text
POST /api/session/{sessionID}/prompt
```

```json
{ "text": "..." }
```

この API のレスポンスは最終回答ではなく、受け付けたユーザー入力です。したがって V1 のように、レスポンスをそのまま答えとして使うことはできません。

model や agent はこのコマンドからは指定せず、OpenCode 側の設定や既定値を使います。

### 3. 完了の待ち合わせ

入力を送っただけでは答えは返りません。対象セッションが処理中かどうかをポーリングして、完了を待ちます。

```text
GET /api/session/active
```

このレスポンスの `data` はセッション ID をキーとしたオブジェクトで、

```json
{ "data": { "ses_...": { "type": "running" } } }
```

のように対象セッションが `running` なら処理中です。対象セッションが `data` から消えると、今回の run は終わっています。ただし、その時点では直ちに「完了」と判断できるとは限りません。

ポーリング間隔は固定値で 500ms です。

対象セッションが `active` に無いという状態は、2 通りあり得ます。1 つは、実行が非常に短く、`POST prompt` の直後に既に完了している場合。もう 1 つは、逆に、agent loop がまだセッションを処理中として登録していない場合です。

そのため `active` に無いだけでは完了と判定しません。セッションを `running` として一度でも観測した場合は、その後に `active` から消えた時点で完了とします。一度も観測していない場合は、prompt 前の baseline より新しい assistant message が既に投影されている場合だけ完了とし、そうでなければポーリングを続けます。

### 4. assistant message の取得

完了したら、今回の run の回答を取得します。

```text
GET /api/session/{sessionID}/message?type=assistant&order=desc&limit=1
```

過去の回答を誤って返さないよう、prompt を送る直前に最新だった assistant message の ID を控えておき、完了後にそれと ID が違う message だけを今回の回答として扱います。同じ ID のままだった場合は、再実行前の回答なので新しい回答とはみなしません。message の反映が完了判定より遅れる場合は、短い間だけ再試行します。

取得した message の `content` のうち、

```json
{ "type": "text", "text": "..." }
```

だけを出力します。reasoning や tool 実行の内容は出力しません。`text` が複数ある場合は、出現順に改行で連結します。

`finish` が `error` の message は、run が OpenCode 側で失敗した message です。`content` は空になるため、text がないことではなく OpenCode が記録した失敗を、そのままエラーとして伝播します。

```json
{
  "content": [],
  "finish": "error",
  "error": {
    "type": "provider.auth",
    "message": "Error from provider (Console): OpenCode's free tier can only be used from within OpenCode",
    "status": 403
  }
}
```

```text
opencode-session: OpenCode run failed: Error from provider (Console): OpenCode's free tier can only be used from within OpenCode
```

`error` や `error.message` が欠落した応答でも panic はせず。原因が分からない場合は汎用の

```text
opencode-session: OpenCode run failed
```

として終了します。`finish` が `error` でないのに text がない場合は、これまでどおり `no text content` として終了します。

### Question (Form) への回答

OpenCode V2 では、旧 `question` 相当の対話は Form として表現されます。このコマンドは Slack 側との双方向 protocol をまだ持たないため、Form への回答は実装していません。

run の待ち合わせ中はセッションの Form も確認し、回答待ちの Form があれば、無限に待つことなく、

```text
unsupported interaction: form
```

というエラーで終了します。

この一覧には回答待ちの Form だけが含まれるため、Form ごとに状態を取りに行く追加のリクエストは不要です。Form の内容を標準出力へ出したり、Slack 固有の応答を生成したり、Form へ自動で回答したりはしません。

permission も同様に、このコマンドからは回答しません。必要な tool は OpenCode 側で allow し、それ以外を deny する運用を想定しています。

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

空の入力は usage error として終了します。

OpenCode CLI のオプションをそのまま渡す機能はありません。

## 環境変数

`SLACK_CHANNEL_ID` と `SLACK_THREAD_TS` は必須です。

| 変数                         | 説明                                                          |
| -------------------------- | ----------------------------------------------------------- |
| `SLACK_CHANNEL_ID`         | Slack のチャンネル ID                                             |
| `SLACK_THREAD_TS`          | Slack のスレッドを識別するタイムスタンプ                                   |
| `OPENCODE_URL`             | `opencode serve` の URL。未設定時は `http://opencode:4096`                  |
| `OPENCODE_SERVER_USERNAME` | 認証用のユーザー名。未設定時は `opencode`                             |
| `OPENCODE_SERVER_PASSWORD` | 認証用のパスワード                                                |

OpenCode V2 のサーバーはパスワードで保護されています。`OPENCODE_SERVER_PASSWORD` が未設定でも起動時にランダムなパスワードが自動生成され、Basic Auth が必須になります。

そのため、OpenCode 側とこのコマンドの両方に同じ固定パスワードを渡す前提で運用します。

```yaml
services:
  opencode:
    environment:
      - OPENCODE_SERVER_PASSWORD=${OPENCODE_SERVER_PASSWORD}

  opencode-session:
    environment:
      - OPENCODE_SERVER_PASSWORD=${OPENCODE_SERVER_PASSWORD}
```

`OPENCODE_SERVER_USERNAME` は未設定なら `opencode` が使われます。どちらの変数も設定がない場合は、認証情報なしでリクエストを送ります。

## 標準出力と標準エラー出力

成功時は、最終的な assistant の text だけを標準出力へ出力します。

```text
stdout:
  最終assistant textのみ
stderr:
  原則空
exit code:
  0
```

失敗時は、標準出力を空のままに、原因を標準エラーへ出力して終了します。

```text
stdout:
  空
stderr:
  人間が原因を判断できるエラー
exit code:
  1
```

usage error は exit code 2 です。標準出力に JSON などの制御用の内容は混在しません。

## タイムアウト

run 全体には 15 分の期限を設けています。polling リクエストごとに同じ長さのタイムアウトは設けず、run 全体の期限でまとめて中断します。個別の HTTP リクエストは 30 秒で打ち切ります。

タイムアウト時は原因を標準エラーへ出して、non-zero で終了します。

## OpenCode 側の準備

あらかじめ OpenCode V2 の `opencode serve` を常駐させておく必要があります。V1 の API への fallback はありません。

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

1つのスレッドに対して複数のセッションが存在する場合、どれを使うか判断できないためエラー終了します。

`slack-commander` から利用する場合は、同じスレッドの処理が同時に実行されないようにしてください。

## エラー時の動作

次のような場合はエラー終了します。

* `SLACK_CHANNEL_ID` または `SLACK_THREAD_TS` が設定されていない
* `opencode serve` に接続できない
* OpenCode API が 2xx 以外の status を返した
* OpenCode API のレスポンスを正しく読み取れない
* 同じタイトルのセッションが複数存在する
* 一致したセッションに ID がない
* pending の Form がある
* 新しい assistant message がない
* 今回の assistant message に `text` が含まれていない

セッションの検索に失敗した場合、それを「セッションが存在しない」とみなして新規作成することはありません。

## 開発

```bash
go test ./...
go vet ./...
golangci-lint run
```

テストでは `httptest.Server` を使って OpenCode V2 API の動作を再現するため、実際の `opencode serve` は必要ありません。

## コンテナイメージ

コンテナイメージは `ko` でビルドします。

`v*` のタグを push すると GitHub Actions が `linux/amd64` と `linux/arm64` のイメージをビルドし、GHCR に公開します。

詳細は `.ko.yaml` と `.github/workflows/ci.yml` を参照してください。
