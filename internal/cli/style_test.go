package cli

import (
	"bytes"
	"fmt"
	"testing"
)

func TestTableIgnoresANSI(t *testing.T) {
	var out bytes.Buffer
	tw := newTable(&out)
	fmt.Fprintf(tw, "%s\tb\tlast\n", "\x1b[31mred\x1b[0m")
	fmt.Fprintf(tw, "longer\tbb\tx\n")
	if err := tw.Flush(); err != nil {
		t.Fatal(err)
	}
	want := "\x1b[31mred\x1b[0m     b   last\nlonger  bb  x\n"
	if out.String() != want {
		t.Errorf("got %q\nwant %q", out.String(), want)
	}
}
