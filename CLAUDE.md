# CLAUDE.md — AI エージェント向けの手引き

Windows の画面を同じ LAN の iPhone（Safari）へ WebRTC で配信する Go 製ツール。詳細は README.md を参照。
この文書は、別の PC で AI に構築・改修を任せるときの要点をまとめたもの。

## 会話・ドキュメントの言語

- 会話、コードコメント、README などのドキュメントは日本語で書く。

## 構築手順（別の PC で）

1. 前提を確認・導入する（無いものだけ）。
   - Go 1.27 以降：`go version`（無ければ `winget install --id GoLang.Go -e`）
   - FFmpeg 8.0 以降：`ffmpeg -hide_banner -filters` に `ddagrab` と `scale_d3d11` があること
     （無ければ `winget install --id Gyan.FFmpeg -e`。PATH の反映にはターミナルの開き直しが必要）
2. ビルドと検証：
   ```powershell
   go mod verify
   go vet ./...
   go test ./...
   go build -o monito.exe ./cmd/monito
   .\monito.exe -list-monitors
   ```
3. 起動は `.\monito.exe`。ターミナルに QR が出る。PC での動作確認は、表示される `http://127.0.0.1:<port>/?t=...` をブラウザで開く。
   ポート 8765 が使用中なら `-port` で変える（テストでは利用者の monito と衝突しないよう別ポートを使う）。

## 任意：拡張ディスプレイ（Virtual Display Driver）

ドライバのインストールは利用者の手作業が前提（管理者権限・ドライバ導入を伴うため、勝手に行わない）。手順は README の「iPhone を拡張ディスプレイにする」。

- `winget install --id=VirtualDrivers.Virtual-Display-Driver -e` は管理アプリを置くだけで、**ドライバ本体は VDD Control の「Install」で入る**。
- 解像度は `C:\VirtualDisplayDriver\vdd_settings.xml` の `<resolutions>` に、利用者の iPhone と同じ解像度を**横・縦の両方**追加し、ドライバを再起動する。
- 終了時にデバイスごと無効化したい場合は、利用者に管理者 PowerShell で `scripts\install-display-helper.ps1` を実行してもらう。
- VDD の名前付きパイプ（`MTTVirtualDisplayPipe`）に `SETDISPLAYCOUNT` などを送らないこと。既存のモニターは消えず、VDD Control が壊れた表示になった。

## 注意点（過去に実際に起きたこと）

- **monito.exe 自身を UAC で昇格させてタスク登録などを行うと、Windows Defender に誤検知される**（`Behavior:Win32/Execution.A!ml`。exe が隔離された）。管理者権限が要る処理は、中身が読める PowerShell スクリプトに分け、利用者に実行してもらう。子プロセスの窓を隠す指定（`CREATE_NO_WINDOW`）も避ける。
- **Git Bash のヒアドキュメントでは `\\` が `\` 1 文字に縮む。** Python などへ置換スクリプトを渡すと、パスの `\v` が制御文字になって README が壊れた。バックスラッシュを含む編集は、エディタツールで直接行うか、スクリプトをファイルに書いてから実行する。
- PowerShell 5.1 は BOM なしの UTF-8 スクリプトを ANSI として読む。日本語を含む `.ps1` は BOM 付き UTF-8 で保存する。
- 管理者権限のウィンドウが前面にあると、SendInput による操作は UIPI で黙って破棄される（エラーにならない）。操作のテストでは前面ウィンドウに注意する。
- 個人情報（PC 名、LAN の IP、ユーザー名、メールアドレス）をコミットしない。例示は `monito-mypc.local`、`192.168.x.x` のように一般化する。

## 構成と設計の要点

- 映像：`ddagrab → scale_d3d11（NV12/BT.709）→ エンコーダ自動選択 → FLV（stdout）→ internal/flv で取り出し → pion の TrackLocalStaticSample`
  - 出力を Annex-B ではなく FLV にしているのは、フレーム終端の確定待ち（1 フレーム分の遅延）をなくすため。
  - PLI は FFmpeg の再起動で代替（CLI では外部から IDR を強制できない）。
- 認証：QR のワンタイムトークン → 端末鍵（`%APPDATA%\Moni-to\devices.json` に SHA-256 のみ保存）。
- 操作：DataChannel `input` → `internal/input`（SendInput、ABSOLUTE|VIRTUALDESK、物理ピクセル。プロセスは Per-Monitor DPI Aware V2）。
- 自動回転・拡大率の切り替え・仮想ディスプレイの有効化は、Virtual Display Driver のモニターに対してだけ行う（実モニターの設定は変えない）。
- テストは `internal/flv`、`internal/h264`、`internal/input`、`internal/capture` にある。変更したら `go vet ./... && go test ./...` を通す。
