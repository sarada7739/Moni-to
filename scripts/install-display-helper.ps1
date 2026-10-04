<#
  Moni-to: 仮想ディスプレイ（Virtual Display Driver）の有効化・無効化タスクを登録する。

  monito は終了時に仮想ディスプレイを無効化し（設定画面からも消える）、起動時に有効化する。
  デバイスの有効・無効には管理者権限が必要なため、このスクリプトで次の 2 つのタスクを登録しておく。
  登録後は monito が通常権限でタスクを起動するだけなので、UAC の確認は出ない。

    \Moni-to\VDD Enable   … pnputil /enable-device  /deviceid "Root\MttVDD"
    \Moni-to\VDD Disable  … pnputil /disable-device /deviceid "Root\MttVDD"

  使い方：PowerShell を「管理者として実行」し、このスクリプトを実行する。
    powershell -ExecutionPolicy Bypass -File .\scripts\install-display-helper.ps1
#>

$ErrorActionPreference = 'Stop'

$principal = New-Object Security.Principal.WindowsPrincipal([Security.Principal.WindowsIdentity]::GetCurrent())
if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
    Write-Host 'このスクリプトは管理者として実行してください（PowerShell を右クリック →「管理者として実行」）。' -ForegroundColor Red
    exit 1
}

if (-not (Get-PnpDevice -ErrorAction SilentlyContinue | Where-Object { $_.HardwareID -contains 'Root\MttVDD' })) {
    Write-Host 'Virtual Display Driver がインストールされていません。先に VDD Control の「Install」でインストールしてください。' -ForegroundColor Red
    exit 1
}

# タスクを実行するユーザー（このスクリプトを実行したユーザー）
$user = [Security.Principal.WindowsIdentity]::GetCurrent().Name

# S4U：ログオン状態に関係なく、画面を出さずに実行する。Highest：管理者権限で実行する
$taskPrincipal = New-ScheduledTaskPrincipal -UserId $user -LogonType S4U -RunLevel Highest
$settings = New-ScheduledTaskSettingsSet -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries `
    -ExecutionTimeLimit (New-TimeSpan -Minutes 1) -MultipleInstances Queue

$tasks = @(
    @{ Name = 'VDD Enable';  Args = '/enable-device /deviceid "Root\MttVDD"';  Desc = 'Moni-to: 仮想ディスプレイ（Virtual Display Driver）を有効化する' },
    @{ Name = 'VDD Disable'; Args = '/disable-device /deviceid "Root\MttVDD"'; Desc = 'Moni-to: 仮想ディスプレイ（Virtual Display Driver）を無効化する' }
)
foreach ($t in $tasks) {
    $action = New-ScheduledTaskAction -Execute "$env:SystemRoot\System32\pnputil.exe" -Argument $t.Args
    Register-ScheduledTask -TaskPath '\Moni-to\' -TaskName $t.Name -Action $action -Principal $taskPrincipal `
        -Settings $settings -Description $t.Desc -Force | Out-Null
    Write-Host "登録しました: \Moni-to\$($t.Name)"
}
Write-Host '完了しました。以降、monito の終了時に仮想ディスプレイは無効化され、設定画面からも消えます。' -ForegroundColor Green
