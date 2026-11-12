package appwin

import "testing"

func TestAppIDFollowsThePath(t *testing.T) {
	a := appID(`C:\Users\me\Apps\z407.exe`)
	if b := appID(`c:\users\me\apps\Z407.EXE`); a != b {
		t.Errorf("same file, different case: %s, %s", a, b)
	}
	if b := appID(`C:\Users\Public\z407-test\z407.exe`); a == b {
		t.Errorf("two copies share the id %s", a)
	}
	if len(a) > 128 {
		t.Errorf("id too long for the taskbar: %d", len(a))
	}
}
