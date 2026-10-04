# Moni-to

Windows PC の画面を、同じ Wi-Fi 上の iPhone（Safari）へ WebRTC で低遅延ストリーミングするツールです。
iPhone は標準カメラで QR コードを読み取るだけで接続できます。外部サーバーは使わず、LAN 内で完結します。

> 現在の状態：**Phase 2** — モニター選択（仮想ディスプレイ対応）と、iPhone からのタッチ操作。

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
**QR を読むのは最初の 1 回だけ**です。ホーム画面のアイコンは、ホストを再起動しても、PC の IP が変わっても使えます。

- QR は確実につながるよう IP の URL（`http://192.168.x.x:8765/`）で表示します。
- 接続後、ページが mDNS 名の URL（`http://monito-<PC名>.local:8765/`）に届くか確かめ、届けば自動でそちらへ移動します。
  この状態で「ホーム画面に追加」してください（アドレスバーが `monito-….local` になっていれば OK）。
- mDNS 名に届かない環境では IP の URL のまま動きます（ターミナルにその旨が表示されます）。
  その場合、PC の IP が変わるとホーム画面に追加し直す必要があります。

## フラグ一覧

| フラグ | 既定値 | 説明 |
|---|---|---|
| `-port` | `8765` | HTTP（TCP）と WebRTC（UDP）で使うポート番号 |
| `-ip` | 自動検出 | LAN の IP アドレス。NIC が複数あって自動選択が違う場合に指定 |
| `-keyint` | `2s` | キーフレーム間隔（例：`1s`, `1500ms`） |
| `-ffmpeg` | 自動 | `ffmpeg.exe` のパス |
| `-encoder` | `auto` | `auto` / `h264_nvenc` / `h264_qsv` / `h264_amf` / `libx264` / `h264_mf` |
| `-name` | `monito-<PC名>` | mDNS のホスト名（`.local` は付けない） |
| `-no-mdns` | `false` | mDNS 名を使わず、IP の URL だけで動かす |
| `-reset-devices` | `false` | 登録済みの端末をすべて削除してから起動する（iPhone を紛失したときなど） |
| `-monitor` | 仮想ディスプレイ → 無ければプライマリ | キャプチャするモニター。番号（`-list-monitors` の左端）、名前（`DISPLAY4` など）、または `virtual` |
| `-keep-display` | `false` | 終了時に仮想ディスプレイを切断しない |
| `-list-monitors` | — | モニター一覧を表示して終了する |
| `-no-input` | `false` | iPhone からの操作を受け付けない（見るだけにする） |
| `-no-rotate` | `false` | iPhone の向きに合わせて仮想モニターの縦横を切り替えない |
| `-scale-landscape` | `200` | 横向き時の仮想モニターの拡大率（%）。`0` で変更しない |
| `-scale-portrait` | `125` | 縦向き時の仮想モニターの拡大率（%）。`0` で変更しない |

fps = 60、ビットレート = 10 Mbps は固定です（Phase 3 でフラグ化予定）。

## iPhone を拡張ディスプレイにする（Virtual Display Driver）

[Virtual Display Driver](https://github.com/VirtualDrivers/Virtual-Display-Driver)（VDD）で仮想モニターを追加し、それを配信します。

1. `winget install --id=VirtualDrivers.Virtual-Display-Driver -e` で取得し、同梱の **VDD Control を管理者として起動して「Install」**を押す
   （winget だけではドライバ本体は入りません）
2. `C:\VirtualDisplayDriver\vdd_settings.xml` の `<resolutions>` に iPhone と同じ解像度を**横向き・縦向きの両方**追加する
   （例：iPhone 18 Pro Max なら `2868x1320` と `1320x2868`。縦向きが無いと自動回転できません）
3. デバイスマネージャー →「ディスプレイ アダプター」→ Virtual Display Driver を無効化 → 有効化
4. Windows の「設定 → ディスプレイ」で「表示画面を拡張する」にし、仮想モニターの解像度を 2 で追加したものにする。
   **拡大率は 200%** を推奨（実質 1434×660。iPhone の画素と 1:1 でくっきり表示されます）
5. `monito.exe` で起動する（`-monitor` を省略すると仮想ディスプレイが選ばれます）

### 仮想ディスプレイの自動接続・切断

- **monito を終了すると仮想ディスプレイをデスクトップから切断**します（Ctrl+C でもコンソールの × ボタンでも）。
  仮想ディスプレイ上のウィンドウは Windows が他のモニターへ移します。ドライバ自体はインストールされたままです。
- 次回起動時に、切断前の解像度・位置で自動的に接続し直します（`%APPDATA%\Moni-to\virtual_display.json` に保存）。
- 切断したくない場合は `-keep-display` を付けて起動してください。
- タスクマネージャーでの強制終了など、monito が後片付けできない終了の仕方では切断されません。

> **拡大率の上限について**：Windows は拡大後の実質的な高さが小さくなりすぎる拡大率を選べないようにしています。
> 例えば 1912×880 では 125% までしか選べません。大きく表示したい場合は解像度を上げて拡大率を上げてください。

### 縦画面（自動回転）

iPhone を縦にすると、仮想モニターの解像度が縦横入れ替わります（例：2868×1320 ⇔ 1320×2868）。切り替えには 1〜2 秒かかります。

- 自動回転するのは **Virtual Display Driver のモニターだけ**です（実モニターの解像度は変更しません）。
- 解像度が変わるため、仮想モニター上のウィンドウの位置・大きさは Windows によって調整されます。
- **拡大率も向きごとに切り替えます**（既定：横 200% / 縦 125%）。縦向きで 200% だと実質の幅が 660px しかなく、
  Discord など幅 940px 前後を前提とするアプリが崩れるためです。125% なら実質 1056px 幅になり、一般的な縦置きモニター（1080px 幅）とほぼ同じ配置になります。
  ただし画面が小さいので文字は小さくなります。好みに応じて `-scale-portrait 150` などに変えてください。
- 拡大率の変更には Windows の**非公開 API**（設定アプリが内部で使うもの）を使っています。Windows の更新で動かなくなる可能性があります。
- 不要なら `-no-rotate` で無効にできます。

## iPhone からの操作

| 操作 | Windows 側の動作 |
|---|---|
| タップ | 左クリック |
| ドラッグ（10px 以上動かす） | 左ボタンを押したまま移動 |
| 長押し（0.5 秒） | 右クリック |
| 2 本指で上下左右に動かす | スクロール（指の動きにページが追従する向き） |

PC のブラウザで確認するときは、マウスで同じ操作ができます（右ボタンは右クリック、ホイールはスクロール）。
座標は映像の表示領域（黒帯を除く）を基準に変換するので、iPhone の向きや比率が違ってもずれません。

## 認証の仕組み

- **ワンタイムトークン**（QR の `?t=`）：1 回使い切り、5 分で失効。使用・失効すると即座に新しい QR が表示されます。
- **端末鍵**（`?s=`）：トークンと引き換えに発行し、端末を登録します。ホストを再起動しても有効です。
  URL を `?s=<鍵>` に書き換えるので、その状態で「ホーム画面に追加」すると鍵付き URL が起動 URL になります。
  - 登録情報は `%APPDATA%\Moni-to\devices.json` に保存されます（鍵そのものではなく SHA-256 のみ）。
  - `-reset-devices` で全端末の登録を取り消せます。
- **同時視聴は 1 台**。新しい端末が接続すると、前の端末は切断されます（後勝ち）。

## mDNS 名について

monito は自前で mDNS に応答し、`monito-<PC名>.local` を LAN の IP **だけ**に解決させます。
Windows 自身も `<PC名>.local` に応答しますが、仮想アダプタ（`169.254.x.x` など）のアドレスまで返すことがあり、
iPhone が到達できない IP を選ぶおそれがあるため、別名にしています。

## 構成

```
cmd/monito/            起動・フラグ解析
internal/netutil/      LAN IP 検出
internal/mdns/         mDNS 応答（monito-<PC名>.local）
internal/auth/         トークン・端末鍵の管理
internal/display/      DXGI によるモニター列挙（cgo なし）
internal/input/        SendInput によるマウス操作と座標変換
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
- **HTTP（非 HTTPS）で配信**：iPhone Safari で WebRTC の受信は確認済み。ホーム画面からの全画面起動は確認中です。
- **キャプチャできない画面**：UAC の確認ダイアログ・ロック画面などのセキュアデスクトップは Desktop Duplication の制約で映りません。FFmpeg が終了した場合は自動で再起動します。
- **プロファイルは Constrained Baseline のみ**：Safari との互換性を優先しています。
- **ポート番号を変えるとホーム画面のアイコンは使えなくなります**（URL にポート番号が含まれるため）。
- **ルーターによっては mDNS が届かない**ことがあります（有線と Wi-Fi の間でマルチキャストを中継しない機種、ゲスト Wi-Fi など）。その場合は IP の URL で動作します。
- **FFmpeg を同梱して配布する場合のライセンス**：GPL ビルド（libx264 を含むもの）を同梱すると GPL の義務が生じます。配布用には LGPL ビルドが無難です（nvenc / qsv / amf は使用可能。libx264 は無いので GPU が無い環境では `h264_mf` を使用）。
- **管理者権限のウィンドウが前面にあると、iPhone からの操作は効きません**（Windows の UIPI による制限。エラーにもならず無視されます）。操作したい場合は monito も管理者として起動してください。
- 操作は指 1〜2 本まで（ピンチ操作、3 本指以上は未対応）。キーボード入力は未対応です。
- `h264_qsv` / `h264_amf` / `h264_mf` は開発環境（NVIDIA GPU）では未検証です。
