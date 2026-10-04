package main

// 仮想ディスプレイの有効化・無効化ヘルパー（タスク スケジューラ）。
//
// デバイスの有効・無効には管理者権限が必要だが、毎回 UAC を出すのは使いにくい。
// そこで最初に 1 回だけ scripts\install-display-helper.ps1 を管理者として実行して
// 「有効化」「無効化」のタスクを登録しておき、monito は通常権限でタスクを起動するだけにする。
//
// 注意：以前は monito.exe 自身が UAC で昇格してタスクを登録していたが、その振る舞いが
// Windows Defender に誤検知（Behavior:Win32/Execution.A!ml）されたため、登録はスクリプトに分離した。

import (
	"fmt"
	"os/exec"
	"strings"
	"time"

	"moni-to/internal/display"
)

const (
	taskEnable  = `\Moni-to\VDD Enable`
	taskDisable = `\Moni-to\VDD Disable`
)

// helperInstalled はヘルパーのタスクが 2 つとも登録済みかを返す。
func helperInstalled() bool {
	for _, t := range []string{taskEnable, taskDisable} {
		if exec.Command("schtasks", "/Query", "/TN", t).Run() != nil {
			return false
		}
	}
	return true
}

// runTask はタスクを起動する（完了は待たない）。
func runTask(name string) error {
	out, err := exec.Command("schtasks", "/Run", "/TN", name).CombinedOutput()
	if err != nil {
		return fmt.Errorf("タスク %s を起動できません: %v %s", name, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// waitVirtual は仮想ディスプレイのデバイスが現れる（present=true）／消える（false）まで待つ。
func waitVirtual(present bool, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, _, ok := display.VirtualDevice(); ok == present {
			return true
		}
		time.Sleep(200 * time.Millisecond)
	}
	return false
}
