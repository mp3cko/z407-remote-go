package media

import "testing"

func TestUnknownKeyError(t *testing.T) {
	if err := unknownKey("zzz"); err == nil || err.Error() != `media: unknown key "zzz"` {
		t.Fatal(err)
	}
}

func TestNewPresser(t *testing.T) {
	if New() == nil {
		t.Fatal("nil")
	}
}
