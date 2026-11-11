//go:build windows

package main

import (
	"fmt"
	"os"

	"github.com/mp3cko/z407-remote-go/internal/logx"
	"golang.org/x/sys/windows"
)

// The release binary is linked with -H=windowsgui so that double-clicking it
// opens no console window. Such a process starts without standard output,
// and setupOutput points it at the log file. A console build, or one whose
// output is redirected or piped, already has working handles and keeps them.
//
// --log overrides this either way. "terminal" makes the GUI build write to
// the console of the terminal it was started from. That is not the default,
// because it only half works: the shell does not wait for a GUI program, so
// the output lands on top of its next prompt, and under WSL that console is
// the Windows terminal itself, behind the back of tmux and with line feeds
// that do not return the carriage.

// windowless is true when the output goes to the log file: nobody is
// watching, so a startup failure has to be shown in a message box.
var windowless bool

const attachParentProcess = ^uint32(0) // ATTACH_PARENT_PROCESS

func handleUsable(std uint32) bool {
	h, err := windows.GetStdHandle(std)
	if err != nil || h == 0 || h == windows.InvalidHandle {
		return false
	}
	t, _ := windows.GetFileType(h)
	return t != windows.FILE_TYPE_UNKNOWN
}

func setupOutput(mode string) {
	usable := handleUsable(windows.STD_OUTPUT_HANDLE) || handleUsable(windows.STD_ERROR_HANDLE)
	switch {
	case mode == logFile:
		if usable {
			announceLogFile()
		}
	case usable:
		return
	case mode == logTerminal && attachParentConsole():
		return
	}
	windowless = true
	if f, err := logToFile(); err == nil {
		setStdHandles(f)
	}
}

// attachParentConsole makes the console of the parent process, if it has
// one, the standard output.
func attachParentConsole() bool {
	attach := windows.NewLazySystemDLL("kernel32.dll").NewProc("AttachConsole")
	if r, _, _ := attach.Call(uintptr(attachParentProcess)); r == 0 {
		return false
	}
	con, err := os.OpenFile("CONOUT$", os.O_RDWR, 0)
	if err != nil {
		return false
	}
	os.Stdout, os.Stderr = con, con
	logx.UseStdout(con)
	setStdHandles(con)
	fmt.Fprintln(con) // the shell has already printed its next prompt
	return true
}

func setStdHandles(f *os.File) {
	_ = windows.SetStdHandle(windows.STD_OUTPUT_HANDLE, windows.Handle(f.Fd()))
	_ = windows.SetStdHandle(windows.STD_ERROR_HANDLE, windows.Handle(f.Fd()))
}

// alertFailure shows a startup failure when there is no console to read it in.
func alertFailure(err error) {
	if !windowless {
		return
	}
	text := fmt.Sprintf("Z407 Remote could not start.\n\n%v\n%s\nLog: %s",
		err, portHint(err), logPath())
	t, err1 := windows.UTF16PtrFromString(text)
	c, err2 := windows.UTF16PtrFromString("Z407 Remote")
	if err1 != nil || err2 != nil {
		return
	}
	_, _ = windows.MessageBox(0, t, c, windows.MB_OK|windows.MB_ICONERROR)
}
