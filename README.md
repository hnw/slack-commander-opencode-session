# slack-commander-opencode-session

`slack-commander` から OpenCode のセッションを継続して利用するための wrapper です。

Slack のスレッドと OpenCode のセッションを対応付け、同じスレッドへの後続投稿では既存のセッションを自動的に再利用します。

対応付けには OpenCode 自身のセッション情報を利用するため、この wrapper はセッション ID などの永続状態を持ちません。

## 仕組み

1. 環境変数 `SLACK_CHANNEL_ID` と `SLACK_THREAD_TS` から、次の形式で OpenCode のセッションタイトルを生成します。

   ```text
   slack:<SLACK_CHANNEL_ID>:<SLACK_THREAD_TS>
   ```

   例:

   ```text
   slack:C01234567:1780000123.456789
   ```

2. OpenCode コンテナで `opencode session list --format json` を実行し、タイトルが完全一致するセッションを探します。

   * 0件: `opencode run --title <title> ...` で新しいセッションを作成
   * 1件: `opencode run --session <session-id> ...` で既存のセッションを継続
   * 2件以上: エラー終了

   検索対象は `opencode session list` が返す範囲です。古いセッションが一覧に含まれない場合は、新しいセッションとして扱います。

3. `opencode run` の標準入力・標準出力・標準エラー出力を wrapper に接続し、OpenCode の終了コードを可能な限りそのまま返します。

`session list` に失敗した場合は、新しいセッションを作成せずエラー終了します。既存セッションを確認できないまま新しいセッションを作ると、同じ Slack スレッドに対応するセッションが重複する可能性があるためです。

新しいセッションの最初の `run` が途中で失敗した場合も、そのセッションを wrapper 側で削除したり修復したりはしません。

## CLI

```text
opencode-session [--service SERVICE] run [OPENCODE RUN ARGS...]
```

`--service` には OpenCode を実行する Compose service 名を指定します。デフォルトは `opencode` です。

`run` より後ろの引数は、原則としてそのまま `opencode run` に渡します。

ただし、セッションの選択に使う次のオプションは wrapper が管理するため指定できません。

```text
--session
-s
--title
```

## 必要な環境変数

| 変数                 | 説明                                        |
| ------------------ | ----------------------------------------- |
| `SLACK_CHANNEL_ID` | Slack のチャンネル ID。未設定または空の場合はエラー            |
| `SLACK_THREAD_TS`  | Slack のスレッドを識別するタイムスタンプ。未設定または空の場合はエラー |

## 実行条件

OpenCode は wrapper と同じコンテナ内では実行しません。

[compose-exec](https://github.com/hnw/compose-exec) を Go ライブラリとして利用し、Compose service として定義された OpenCode コンテナを sibling container として起動します。`docker compose` CLI や外部の `compose-exec` コマンドは呼び出しません。

この構成では、次の条件を満たす必要があります。

* wrapper を実行するコンテナから Docker socket にアクセスできること
* wrapper を実行する service と OpenCode の service が同じ Compose project に定義されていること
* wrapper の current working directory から Compose project を読み込めること

Compose project の読み込みでは、`compose.yaml` や `docker-compose.yml` など、通常の Compose 設定ファイルを使用します。

Docker-outside-of-Docker 構成では、Compose project のディレクトリが host と wrapper コンテナの両方で同じ絶対パスになるように mount してください。Compose の bind mount の source は host 側のパスとして解決されるためです。

## 使い方

`slack-commander` の設定例:

```toml
[[commands]]
keyword = "opencode *"
command = "opencode-session run *"
runner = "compose"
tty = true
timeout = 3600
reply_broadcast = false
```

Compose project には、wrapper を実行する service と OpenCode を実行する service を定義します。

```yaml
services:
  slack-commander:
    image: your/slack-commander:latest
    working_dir: /project
    volumes:
      - .:/project
      - /var/run/docker.sock:/var/run/docker.sock
    environment:
      - SLACK_CHANNEL_ID
      - SLACK_THREAD_TS

  opencode:
    image: ghcr.io/sst/opencode:latest
    # OpenCode の永続データ、認証情報、設定などは
    # この service 側で管理する
    # 例: config や cache の volume mount
```

OpenCode の認証情報、設定、セッションデータなどは wrapper では管理せず、`opencode` service 側で管理します。

## 開発

```bash
go test ./...
golangci-lint run
```
