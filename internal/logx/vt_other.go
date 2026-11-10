//go:build !windows

package logx

import "os"

func enableVT(*os.File) bool { return true }
