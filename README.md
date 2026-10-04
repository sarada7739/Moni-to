# Moni-to

Windows PC の画面を、同じ Wi-Fi 上の iPhone（Safari）へ WebRTC で低遅延ストリーミングするツールです。
iPhone は標準カメラで QR コードを読み取るだけで接続できます。外部サーバーは使わず、LAN 内で完結します。

> 現在の状態：**Phase 3** — 遅延チューニング（FLV 出力・受信バッファ最小化）、統計オーバーレイ、自動再接続、解像度・fps・ビットレートの指定。

## 必要なもの

| 対象 | 必要なもの |
|---|---|
| ホスト PC | Windows 10/11（64bit）、**FFmpeg 8.0 以降**（`ddagrab` と `scale_d3d11` を含む Windows 向けビルド） |
| ビルドする場合のみ | Go 1.27 以降、Git |
| 拡張ディスプレイにする場合 | [Virtual Display Driver](https://github.com/VirtualDrivers/Virtual-Display-Driver)（後述） |
| 視聴側 | iPhone の Safari（同じ PC の Chrome / Edge でも確認可） |

FFmpeg は次の順で探します：`-ffmpeg` で指定したパス → `monito.exe` と同じフォルダの `ffmpeg.exe` → PATH。
見つからない場合や、古くて必要な機能が無い場合は、起動時にエラーメッセージで案内します。

## 別の PC で構築する

```powershell
winget install --id GoLang.Go -e          # Go（未インストールの場合）
winget install --id Gyan.FFmpeg -e        # FFmpeg 8.0 以降
# ↑ インストール後はターミナルを開き直す（PATH を反映するため）
git clone https://github.com/sarada7739/Moni-to.git
cd Moni-to
go build -o monito.exe ./cmd/monito
.\monito.exe
```

- 動作確認：`go vet ./...` と `go test ./...` が通り、`.\monito.exe -list-monitors` でモニター一覧が出れば OK です。
- **Windows Defender が monito.exe を誤検知することがあります**（署名のない自作 exe のため）。隔離された場合は、Windows セキュリティの「保護の履歴」から復元するか、ビルドしたフォルダを除外に追加してください。
- **ファイアウォール**：初回起動時の確認で許可してください。ネットワークが「パブリック」扱いの PC では、パブリックも許可しないと iPhone から接続できません。
- iPhone から接続するには、PC と iPhone が同じルーター（同じ LAN）につながっている必要があります（ゲスト Wi-Fi や AP アイソレーションでは不可）。
- AI（Claude Code など）に構築を任せる場合は、リポジトリ直下の `CLAUDE.md` を読ませてください。

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
3. 初回起動時に Windows ファイアウォールの確認が出たら許可してください（TCP/UDP とも同じポートを使います）。
   ネットワークが「パブリック」扱いになっている場合は、パブリックも許可が必要です。

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
| `-keep-display` | `false` | 終了時に仮想ディスプレイを無効化・切断しない |
| `-list-monitors` | — | モニター一覧を表示して終了する |
| `-no-input` | `false` | iPhone からの操作を受け付けない（見るだけにする） |
| `-no-rotate` | `false` | iPhone の向きに合わせて仮想モニターの縦横を切り替えない |
| `-scale-landscape` | `200` | 横向き時の仮想モニターの拡大率（%）。`0` で変更しない |
| `-scale-portrait` | `125` | 縦向き時の仮想モニターの拡大率（%）。`0` で変更しない |

### 画質・遅延の調整

| フラグ | 既定値 | 説明 |
|---|---|---|
| `-fps` | `60` | フレームレート |
| `-bitrate` | `0`（自動） | ビットレート（kbps）。自動のときは出力解像度×fps から決めます（1080p60 で約 7.5 Mbps、2868×1320@60 で約 13.6 Mbps） |
| `-resolution` | 元のまま | 出力の最大解像度（例：`1920x1080`）。縦横比を保って GPU 上で縮小します。縦長の画面では縦横を入れ替えて当てはめます |
| `-keyint` | `2s` | キーフレーム間隔 |

Wi-Fi が混んでいてカクつく場合は、`-bitrate` を下げるか `-resolution` で縮小してください。

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

### 仮想ディスプレイの自動有効化・無効化

monito を終了すると仮想ディスプレイを片付け、次回起動時に元の解像度・位置で戻します
（Ctrl+C でもコンソールの × ボタンでも。配置は `%APPDATA%\Moni-to\virtual_display.json` に保存）。
仮想ディスプレイ上のウィンドウは Windows が他のモニターへ移します。

片付け方は、ヘルパーを登録しているかで変わります。

| | 終了時の動作 | 設定画面の「ディスプレイ」 |
|---|---|---|
| **ヘルパー登録済み（推奨）** | デバイスごと無効化 | **完全に消える** |
| 未登録 | デスクトップから切断するだけ | 切断中として残る |

**ヘルパーの登録（初回のみ）**：PowerShell を「管理者として実行」し、リポジトリのフォルダで次を実行します。

```powershell
powershell -ExecutionPolicy Bypass -File .\scripts\install-display-helper.ps1
```

タスク スケジューラに `\Moni-to\VDD Enable` / `\Moni-to\VDD Disable`（中身は `pnputil` で Virtual Display Driver を有効化・無効化するだけ）を登録します。
以降は monito が通常権限でタスクを起動するので、UAC の確認は出ません。
削除するときは `scripts\uninstall-display-helper.ps1` を同様に実行します（仮想ディスプレイは有効に戻ります）。

- 何もしたくない場合は `-keep-display` を付けて起動してください。
- タスクマネージャーでの強制終了など、monito が後片付けできない終了の仕方では片付けられません。
- タスクの登録を monito.exe 自身（UAC で昇格）で行う方式は、Windows Defender に誤検知（`Behavior:Win32/Execution.A!ml`）されたため採用していません。

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

## 統計オーバーレイと自動再接続

- **3 本指でタップ**（PC では **S キー**）すると、左上に受信状況を表示します。もう一度で消えます（状態は端末に保存）。
  - 解像度・fps、ビットレート（目標値）、ジッタバッファの遅延、デコード時間、RTT、パケットロス、NACK / PLI の回数、フリーズ回数
  - 「受信側の遅延」は RTT/2 ＋ ジッタバッファ ＋ デコードの目安です（キャプチャ・エンコードの時間は含みません）
- 接続が切れると、1 → 2 → 5 秒間隔で**つながるまで自動で再接続**します（「今すぐ再接続」ボタンもあります）。
  monito を後から起動した場合も、ホーム画面のアイコンを開いたままにしておけば自動でつながります。
- 別の端末に切り替えられた場合と、未登録の端末の場合は再接続しません。

## iPhone からの操作

| 操作 | Windows 側の動作 |
|---|---|
| タップ | 左クリック |
| ドラッグ（10px 以上動かす） | 左ボタンを押したまま移動 |
| 長押し（0.5 秒） | 右クリック |
| 2 本指で上下左右に動かす | スクロール（指の動きにページが追従する向き） |
| 3 本指でタップ | 統計オーバーレイの表示切り替え（Windows には送りません） |

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
internal/capture/      FFmpeg の起動・エンコーダ自動選択・再起動・出力サイズとビットレートの決定
internal/flv/          FFmpeg の FLV 出力から H.264 フレームを取り出す
internal/h264/         NAL ユニットとアクセスユニット（Annex-B への変換）
internal/stream/       pion/webrtc の PeerConnection・PLI 処理
internal/server/       HTTP 配信・WebSocket シグナリング
internal/ui/           ターミナル QR 表示
web/static/            クライアント（素の HTML/JS、exe に埋め込み）
```

映像の流れ：`ddagrab（Desktop Duplication）→ scale_d3d11（GPU で NV12 へ変換・縮小）→ エンコーダ → FLV（stdout）→ フレーム取り出し・Annex-B 化 → TrackLocalStaticSample → iPhone`

- 当初の仕様では FFmpeg の出力を H.264 Annex-B としていましたが、Annex-B はフレームの終わりが「次のフレームの先頭」が届くまで確定せず、
  1 フレーム分（60fps で約 16ms）遅れるため、**データ長が先頭に書かれている FLV に変更**しました（Phase 3）。pion へは Annex-B に直して渡します。
- 色変換は GPU（D3D11 ビデオプロセッサ）で BT.709 で行い、映像にも BT.709 と明記しています（受信側の解釈違いによる色ずれを防ぐため）。
- エンコーダの VBV バッファは 0.1 秒分です（送出のばらつき＝遅延を抑えるため。キーフレームの画質はやや落ちます）。
- 受信側は対応ブラウザでジッタバッファの目標を 0 にしています（`jitterBufferTarget`）。

エンコーダは `h264_nvenc → h264_qsv → h264_amf → libx264 → h264_mf` の順に、**実際に数フレームエンコードして**使えるものを選びます
（`ffmpeg -encoders` に出ていてもハードウェアが無ければ使えないため）。

## 既知の制限

- **PLI（キーフレーム要求）は FFmpeg の再起動で代替**：FFmpeg の CLI には実行中に外部から IDR を強制する手段がないためです。再起動中の数百 ms は映像が止まります。次の定期 IDR が 0.5 秒以内に来る場合や、1 秒以内の連続要求は無視します。
- **HTTP（非 HTTPS）で配信**：iPhone Safari での WebRTC 受信・ホーム画面からの全画面起動とも、HTTP で動作を確認済みです。
- **キャプチャできない画面**：UAC の確認ダイアログ・ロック画面などのセキュアデスクトップは Desktop Duplication の制約で映りません。FFmpeg が終了した場合は自動で再起動します。
- **プロファイルは Constrained Baseline のみ**：Safari との互換性を優先しています。
- **ポート番号を変えるとホーム画面のアイコンは使えなくなります**（URL にポート番号が含まれるため）。
- **ルーターによっては mDNS が届かない**ことがあります（有線と Wi-Fi の間でマルチキャストを中継しない機種、ゲスト Wi-Fi など）。その場合は IP の URL で動作します。
- **FFmpeg を同梱して配布する場合のライセンス**：GPL ビルド（libx264 を含むもの）を同梱すると GPL の義務が生じます。配布用には LGPL ビルドが無難です（nvenc / qsv / amf は使用可能。libx264 は無いので GPU が無い環境では `h264_mf` を使用）。
- **管理者権限のウィンドウが前面にあると、iPhone からの操作は効きません**（Windows の UIPI による制限。エラーにもならず無視されます）。操作したい場合は monito も管理者として起動してください。
- 操作は指 1〜2 本まで（ピンチ操作、3 本指以上は未対応）。キーボード入力は未対応です。
- `h264_qsv` / `h264_amf` / `h264_mf` は開発環境（NVIDIA GPU）では未検証です。
