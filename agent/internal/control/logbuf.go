package control

import (
	"sync"
	"time"
)

const defaultLogBufSize = 1000

// LogLine is a ring-buffer log entry (maps cleanly to agentv1.LogLine).
type LogLine struct {
	TsUnixMs int64
	Level    string
	Message  string
}

// LogBuf is a fixed-size ring buffer of log lines with live subscribers.
type LogBuf struct {
	mu      sync.Mutex
	buf     []LogLine
	size    int
	next    int
	count   int
	subs    map[int]chan LogLine
	subNext int
}

func NewLogBuf(size int) *LogBuf {
	if size <= 0 {
		size = defaultLogBufSize
	}
	return &LogBuf{
		buf:  make([]LogLine, size),
		size: size,
		subs: make(map[int]chan LogLine),
	}
}

func (b *LogBuf) Append(level, message string) {
	line := LogLine{
		TsUnixMs: time.Now().UnixMilli(),
		Level:    level,
		Message:  message,
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf[b.next] = line
	b.next = (b.next + 1) % b.size
	if b.count < b.size {
		b.count++
	}
	for _, ch := range b.subs {
		select {
		case ch <- line:
		default:
			// Slow subscriber: drop this line for them to avoid blocking Append.
		}
	}
}

// Tail returns up to n most recent lines (oldest first). n <= 0 returns all stored lines.
func (b *LogBuf) Tail(n int) []LogLine {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.count == 0 {
		return nil
	}
	if n <= 0 || n > b.count {
		n = b.count
	}
	out := make([]LogLine, n)
	start := (b.next - n + b.size) % b.size
	for i := 0; i < n; i++ {
		out[i] = b.buf[(start+i)%b.size]
	}
	return out
}

// Subscribe returns a channel of live lines and a cancel function.
// The channel is closed when cancel is called.
func (b *LogBuf) Subscribe() (<-chan LogLine, func()) {
	ch := make(chan LogLine, 64)
	b.mu.Lock()
	id := b.subNext
	b.subNext++
	b.subs[id] = ch
	b.mu.Unlock()

	var once sync.Once
	cancel := func() {
		once.Do(func() {
			b.mu.Lock()
			delete(b.subs, id)
			b.mu.Unlock()
			close(ch)
		})
	}
	return ch, cancel
}
