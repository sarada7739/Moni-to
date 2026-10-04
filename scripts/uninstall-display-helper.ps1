<#
  Moni-to: install-display-helper.ps1 で登録したタスクを削除し、仮想ディスプレイを有効に戻す。

  使い方：PowerShell を「管理者として実行」し、このスクリプトを実行する。
    powershell -ExecutionPolicy Bypass -File .\scripts\uninstall-display-helper.ps1
#>

$ErrorActionPreference = 'Stop'

$principal = New-Object Security.Principal.WindowsPrincipal([Security.Principal.WindowsIdentity]::GetCurrent())
if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
    Write-Host 'このスクリプトは管理者として実行してください（PowerShell を右クリック →「管理者として実行」）。' -ForegroundColor Red
    exit 1
}

foreach ($name in 'VDD Enable', 'VDD Disable') {
    if (Get-ScheduledTask -TaskPath '\Moni-to\' -TaskName $name -ErrorAction SilentlyContinue) {
        Unregister-ScheduledTask -TaskPath '\Moni-to\' -TaskName $name -Confirm:$false
        Write-Host "削除しました: \Moni-to\$name"
    }
}

# 無効のまま残らないよう有効に戻す
& "$env:SystemRoot\System32\pnputil.exe" /enable-device /deviceid 'Root\MttVDD' | Out-Null
Write-Host '完了しました（仮想ディスプレイは有効に戻しました）。' -ForegroundColor Green
