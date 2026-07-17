//go:build windows

package tools

import (
	"fmt"
	"os/exec"
	"syscall"
	"unsafe"
)

const (
	createSuspended              = 0x00000004
	createNewProcessGroup        = 0x00000200
	jobObjectExtendedLimitInfo   = 9
	jobObjectLimitKillOnJobClose = 0x00002000
	processTerminate             = 0x0001
	processSetQuota              = 0x0100
	threadSuspendResume          = 0x0002
	threadSnapshot               = 0x00000004
	invalidHandleValue           = ^uintptr(0)
)

var (
	kernel32                    = syscall.NewLazyDLL("kernel32.dll")
	procCreateJobObject         = kernel32.NewProc("CreateJobObjectW")
	procSetInformationJobObject = kernel32.NewProc("SetInformationJobObject")
	procAssignProcessToJob      = kernel32.NewProc("AssignProcessToJobObject")
	procTerminateJob            = kernel32.NewProc("TerminateJobObject")
	procOpenProcess             = kernel32.NewProc("OpenProcess")
	procCreateThreadSnapshot    = kernel32.NewProc("CreateToolhelp32Snapshot")
	procThreadFirst             = kernel32.NewProc("Thread32First")
	procThreadNext              = kernel32.NewProc("Thread32Next")
	procOpenThread              = kernel32.NewProc("OpenThread")
	procResumeThread            = kernel32.NewProc("ResumeThread")
)

type windowsJob struct {
	handle syscall.Handle
}

type ioCounters struct {
	ReadOperationCount  uint64
	WriteOperationCount uint64
	OtherOperationCount uint64
	ReadTransferCount   uint64
	WriteTransferCount  uint64
	OtherTransferCount  uint64
}

type jobExtendedLimitInformation struct {
	BasicLimitInformation jobBasicLimitInformation
	IoInfo                ioCounters
	ProcessMemoryLimit    uintptr
	JobMemoryLimit        uintptr
	PeakProcessMemoryUsed uintptr
	PeakJobMemoryUsed     uintptr
}

type threadEntry struct {
	Size           uint32
	Usage          uint32
	ThreadID       uint32
	OwnerProcessID uint32
	BasePriority   int32
	DeltaPriority  int32
	Flags          uint32
}

func startCommandInJob(cmd *exec.Cmd) (*windowsJob, error) {
	job, err := newWindowsJob()
	if err != nil {
		return nil, err
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createSuspended | createNewProcessGroup}
	if err := cmd.Start(); err != nil {
		_ = job.Close()
		return nil, err
	}
	if err := job.assign(cmd.Process.Pid); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		_ = job.Close()
		return nil, err
	}
	if err := resumePrimaryThread(cmd.Process.Pid); err != nil {
		_ = job.Close()
		_ = cmd.Wait()
		return nil, err
	}
	return job, nil
}

func newWindowsJob() (*windowsJob, error) {
	handle, _, callErr := procCreateJobObject.Call(0, 0)
	if handle == 0 {
		return nil, windowsCallError("CreateJobObjectW", callErr)
	}
	job := &windowsJob{handle: syscall.Handle(handle)}
	if err := job.setKillOnClose(true); err != nil {
		_ = job.Close()
		return nil, err
	}
	return job, nil
}

func (j *windowsJob) Terminate() error {
	ok, _, callErr := procTerminateJob.Call(uintptr(j.handle), 1)
	if ok == 0 {
		return windowsCallError("TerminateJobObject", callErr)
	}
	return nil
}

func (j *windowsJob) Release() error {
	if err := j.setKillOnClose(false); err != nil {
		return err
	}
	return j.Close()
}

func (j *windowsJob) setKillOnClose(enabled bool) error {
	info := jobExtendedLimitInformation{}
	if enabled {
		info.BasicLimitInformation.LimitFlags = jobObjectLimitKillOnJobClose
	}
	ok, _, callErr := procSetInformationJobObject.Call(
		uintptr(j.handle),
		jobObjectExtendedLimitInfo,
		uintptr(unsafe.Pointer(&info)),
		unsafe.Sizeof(info),
	)
	if ok == 0 {
		return windowsCallError("SetInformationJobObject", callErr)
	}
	return nil
}

func (j *windowsJob) assign(pid int) error {
	process, _, callErr := procOpenProcess.Call(processTerminate|processSetQuota, 0, uintptr(uint32(pid)))
	if process == 0 {
		return windowsCallError("OpenProcess", callErr)
	}
	defer syscall.CloseHandle(syscall.Handle(process))
	ok, _, callErr := procAssignProcessToJob.Call(uintptr(j.handle), process)
	if ok == 0 {
		return windowsCallError("AssignProcessToJobObject", callErr)
	}
	return nil
}

func (j *windowsJob) Close() error {
	if j.handle == 0 {
		return nil
	}
	err := syscall.CloseHandle(j.handle)
	j.handle = 0
	return err
}

func resumePrimaryThread(pid int) error {
	snapshot, _, callErr := procCreateThreadSnapshot.Call(threadSnapshot, 0)
	if snapshot == invalidHandleValue {
		return windowsCallError("CreateToolhelp32Snapshot", callErr)
	}
	defer syscall.CloseHandle(syscall.Handle(snapshot))

	entry := threadEntry{Size: uint32(unsafe.Sizeof(threadEntry{}))}
	ok, _, callErr := procThreadFirst.Call(snapshot, uintptr(unsafe.Pointer(&entry)))
	for ok != 0 {
		if entry.OwnerProcessID == uint32(pid) {
			return resumeThread(entry.ThreadID)
		}
		entry.Size = uint32(unsafe.Sizeof(threadEntry{}))
		ok, _, callErr = procThreadNext.Call(snapshot, uintptr(unsafe.Pointer(&entry)))
	}
	if callErr != syscall.ERROR_NO_MORE_FILES {
		return windowsCallError("Thread32Next", callErr)
	}
	return fmt.Errorf("primary thread for process %d not found", pid)
}

func resumeThread(threadID uint32) error {
	thread, _, callErr := procOpenThread.Call(threadSuspendResume, 0, uintptr(threadID))
	if thread == 0 {
		return windowsCallError("OpenThread", callErr)
	}
	defer syscall.CloseHandle(syscall.Handle(thread))
	result, _, callErr := procResumeThread.Call(thread)
	if result == uintptr(^uint32(0)) {
		return windowsCallError("ResumeThread", callErr)
	}
	return nil
}

func windowsCallError(name string, err error) error {
	if err == nil || err == syscall.Errno(0) {
		return fmt.Errorf("%s failed", name)
	}
	return fmt.Errorf("%s: %w", name, err)
}
