package control

import (
	"fmt"
	"sync"
	"testing"
)

func TestLogBufWriterSplitsLines(t *testing.T) {
	buf := NewLogBuf(10)
	w := buf.Writer("info")

	if _, err := w.Write([]byte("hel")); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("lo\nwor")); err != nil {
		t.Fatal(err)
	}
	lines := buf.Tail(0)
	if len(lines) != 1 || lines[0].Message != "hello" {
		t.Fatalf("partial writes: %+v", lines)
	}

	if _, err := w.Write([]byte("ld\r\n\nlast\n")); err != nil {
		t.Fatal(err)
	}
	lines = buf.Tail(0)
	want := []string{"hello", "world", "last"}
	if len(lines) != len(want) {
		t.Fatalf("lines = %+v", lines)
	}
	for i, line := range lines {
		if line.Message != want[i] || line.Level != "info" {
			t.Fatalf("line %d = %+v", i, line)
		}
	}
	// Seq is assigned in append order, starting at 1.
	for i, line := range lines {
		if line.Seq != uint64(i+1) {
			t.Fatalf("line %d seq = %d", i, line.Seq)
		}
	}
}

func TestLogBufWriterConcurrent(t *testing.T) {
	buf := NewLogBuf(1000)
	w := buf.Writer("info")
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				fmt.Fprintf(w, "g%d-line%d\n", id, i)
			}
		}(g)
	}
	wg.Wait()
	if got := len(buf.Tail(0)); got != 400 {
		t.Fatalf("expected 400 lines, got %d", got)
	}
}
