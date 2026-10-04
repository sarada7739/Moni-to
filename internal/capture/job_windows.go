//go:build windows

package capture

import (
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

// ホストが異常終了しても FFmpeg が残らないよう、子プロセスを
// 「ジョブ終了時に強制終了」設定のジョブオブジェクトに入れる。
var (
	jobOnce   sync.Once
	jobHandle windows.Handle
)

func killOnExitJob() windows.Handle {
	jobOnce.Do(func() {
		h, err := windows.CreateJobObject(nil, nil)
		if err != nil {
			return
		}
		info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
		info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
		if _, err := windows.SetInformationJobObject(h, windows.JobObjectExtendedLimitInformation,
			uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
			windows.CloseHandle(h)
			return
		}
		jobHandle = h
	})
	return jobHandle
}

// attachToJob は pid のプロセスをジョブに入れる（失敗しても致命的ではない）。
func attachToJob(pid int) {
	job := killOnExitJob()
	if job == 0 {
		return
	}
	ph, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		return
	}
	defer windows.CloseHandle(ph)
	windows.AssignProcessToJobObject(job, ph)
}
