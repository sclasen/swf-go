package panicinfo

import (
	"strings"
	"testing"
)

var file, name string
var line int

func TestPanicHandler(t *testing.T) {
	panicFunc()
	// Line needs to match our panic call below!
	if !strings.HasSuffix(file, "panic_test.go") || !strings.HasSuffix(name, "panicFunc") || line != 25 {
		t.Errorf("Panic handler did not collect expected information: file=%s name=%s line=%d]", file, name, line)
	}
}

func panicFunc() {
	defer func() {
		// Be safe in here
		r := recover()
		file, line, name = LocatePanic(r)
	}()
	panic("lol I paniced")
}
