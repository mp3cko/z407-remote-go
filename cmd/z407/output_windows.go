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
// and setupOutput points it at a log file. A console build, or one whose
// output is redirected or piped, already has working handles and is left
// alone.
//
// The console of the terminal the program was started from is deliberately
// not used: the shell does not wait for a GUI program, so the output would
// land on top of its next prompt, and under WSL that console is the Windows
// terminal itself, behind the back of tmux and with line feeds that do not
// return the carriage.

// windowless is true when the output goes to the log file: nobody is
// watching, so a startup failure has to be shown in a message box.
var windowless bool

func handleUsable(std uint32) bool {
	h, err := windows.GetStdHandle(std)
	if err != nil || h == 0 || h == windows.InvalidHandle {
		return false
	}
	t, _ := windows.GetFileType(h)
	return t != windows.FILE_TYPE_UNKNOWN
}

func setupOutput() {
	if handleUsable(windows.STD_OUTPUT_HANDLE) || handleUsable(windows.STD_ERROR_HANDLE) {
		return
	}
	windowless = true
	if f, err := openLogFile(logFilePath(os.Getenv("LOCALAPPDATA"), os.TempDir())); err == nil {
		useOutput(f)
	}
}

func useOutput(f *os.File) {
	os.Stdout, os.Stderr = f, f
	_ = windows.SetStdHandle(windows.STD_OUTPUT_HANDLE, windows.Handle(f.Fd()))
	_ = windows.SetStdHandle(windows.STD_ERROR_HANDLE, windows.Handle(f.Fd()))
	logx.UseStdout(f)
}

// alertFailure shows a startup failure when there is no console to read it in.
func alertFailure(err error) {
	if !windowless {
		return
	}
	text := fmt.Sprintf("Z407 Remote could not start.\n\n%v\n%s\nLog: %s",
		err, portHint(err), logFilePath(os.Getenv("LOCALAPPDATA"), os.TempDir()))
	t, err1 := windows.UTF16PtrFromString(text)
	c, err2 := windows.UTF16PtrFromString("Z407 Remote")
	if err1 != nil || err2 != nil {
		return
	}
	_, _ = windows.MessageBox(0, t, c, windows.MB_OK|windows.MB_ICONERROR)
}
