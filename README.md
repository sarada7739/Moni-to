# Moni-to

Windows PC の画面を、同じ Wi-Fi 上の iPhone（Safari）へ WebRTC で低遅延ストリーミングするツールです。
iPhone は標準カメラで QR コードを読み取るだけで接続できます。外部サーバーは使わず、LAN 内で完結します。

> 現在の状態：**Phase 1（最小構成）** — プライマリモニターを配信し、QR で接続して映るところまで。

## 必要なもの

| 対象 | 必要なもの |
|---|---|
| ホスト PC | Windows 10/11、**FFmpeg**（`ddagrab` を含むビルド。FFmpeg 6.1 以降） |
| ビルドする場合のみ | Go 1.27 以降 |
| 視聴側 | iPhone の Safari（同じ PC の Chrome / Edge でも確認可） |

FFmpeg は次の順で探します：`-ffmpeg` で指定したパス → `monito.exe` と同じフォルダの `ffmpeg.exe` → PATH。
見つからない場合はエラーメッセージで案内します（例：`winget install Gyan.FFmpeg`）。

## ビルド

```bash
go build -o monito.exe ./cmd/monito
```

クライアント（HTML/JS）は exe に埋め込まれるので、配布物は `monito.exe` だけです（＋ FFmpeg）。

## 起動方法

コマンドプロンプト（cmd.exe）の場合：

```bat
monito.exe
```

PowerShell の場合：

```powershell
.\monito.exe
```

フラグを付ける例：`monito.exe -port 9000 -keyint 1s`

1. 起動すると LAN IP の候補・モニター一覧・使用エンコーダが表示され、ターミナルに QR コードが出ます。
2. iPhone の標準カメラで QR を読み取り、表示されたリンクを開くと画面が映ります。
3. 初回起動時に Windows ファイアウォールの確認が出たら **「プライベート ネットワーク」を許可**してください（TCP/UDP とも同じポートを使います）。

### PC のブラウザで確認する

ターミナルに `PC で確認する場合: http://127.0.0.1:8765/?t=...` が表示されるので、それを Chrome / Edge で開きます。
（自分の画面を映すので合わせ鏡になりますが、動作確認には十分です）

### ホーム画面に追加（全画面表示）

接続後、Safari の共有メニューから「ホーム画面に追加」すると、アドレスバーなしの全画面で起動できます。
ホーム画面のアイコンは **ホストを再起動するまで** QR なしで再接続できます（後述のセッション鍵）。

## フラグ一覧

| フラグ | 既定値 | 説明 |
|---|---|---|
| `-port` | `8765` | HTTP（TCP）と WebRTC（UDP）で使うポート番号 |
| `-ip` | 自動検出 | LAN の IP アドレス。NIC が複数あって自動選択が違う場合に指定 |
| `-keyint` | `2s` | キーフレーム間隔（例：`1s`, `1500ms`） |
| `-ffmpeg` | 自動 | `ffmpeg.exe` のパス |
| `-encoder` | `auto` | `auto` / `h264_nvenc` / `h264_qsv` / `h264_amf` / `libx264` / `h264_mf` |

Phase 1 では fps = 60、ビットレート = 10 Mbps、対象 = プライマリモニターで固定です（Phase 2・3 でフラグ化予定）。

## 認証の仕組み

- **ワンタイムトークン**（QR の `?t=`）：1 回使い切り、5 分で失効。使用・失効すると即座に新しい QR が表示されます。
- **セッション鍵**（`?s=`）：トークンと引き換えに発行。ホストのプロセスが終了するまで有効。
  URL を `?s=<鍵>` に書き換えるので、その状態で「ホーム画面に追加」すると鍵付き URL が起動 URL になります。
- **同時視聴は 1 台**。新しい端末が接続すると、前の端末は切断されます（後勝ち）。

## 構成

```
cmd/monito/            起動・フラグ解析
internal/netutil/      LAN IP 検出
internal/auth/         トークン・セッション管理
internal/display/      DXGI によるモニター列挙（cgo なし）
internal/capture/      FFmpeg の起動・エンコーダ自動選択・再起動
internal/h264/         Annex-B の NAL 分割とフレーム単位へのまとめ
internal/stream/       pion/webrtc の PeerConnection・PLI 処理
internal/server/       HTTP 配信・WebSocket シグナリング
internal/ui/           ターミナル QR 表示
web/static/            クライアント（素の HTML/JS、exe に埋め込み）
```

映像の流れ：`ddagrab（Desktop Duplication）→ エンコーダ → H.264 Annex-B（stdout）→ NAL 分割 → TrackLocalStaticSample → iPhone`

エンコーダは `h264_nvenc → h264_qsv → h264_amf → libx264 → h264_mf` の順に、**実際に数フレームエンコードして**使えるものを選びます
（`ffmpeg -encoders` に出ていてもハードウェアが無ければ使えないため）。

## 既知の制限

- **フレーム境界による最大 1 フレームの遅延**：Annex-B ストリームでは、フレームの終わりが「次のフレームの先頭」が届くまで確定しません。60fps で約 16ms です。
- **PLI（キーフレーム要求）は FFmpeg の再起動で代替**：FFmpeg の CLI には実行中に外部から IDR を強制する手段がないためです。再起動中の数百 ms は映像が止まります。次の定期 IDR が 0.5 秒以内に来る場合や、1 秒以内の連続要求は無視します。
- **HTTP（非 HTTPS）で配信**：iPhone Safari で WebRTC の受信・ホーム画面からの全画面起動が HTTP でも動くかは実機で確認中です。
- **キャプチャできない画面**：UAC の確認ダイアログ・ロック画面などのセキュアデスクトップは Desktop Duplication の制約で映りません。FFmpeg が終了した場合は自動で再起動します。
- **プロファイルは Constrained Baseline のみ**：Safari との互換性を優先しています。
- **セッション鍵はホストの再起動で無効**になります。再起動後は QR を読み直してください。
- **FFmpeg を同梱して配布する場合のライセンス**：GPL ビルド（libx264 を含むもの）を同梱すると GPL の義務が生じます。配布用には LGPL ビルドが無難です（nvenc / qsv / amf は使用可能。libx264 は無いので GPU が無い環境では `h264_mf` を使用）。
- `h264_qsv` / `h264_amf` / `h264_mf` は開発環境（NVIDIA GPU）では未検証です。
