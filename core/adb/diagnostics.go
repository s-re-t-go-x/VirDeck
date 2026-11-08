package adb

import (
	"strings"
	"sync"
)

type adbOutputTail struct {
	mu   sync.Mutex
	data []byte
}

func (b *adbOutputTail) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := len(p)
	if len(p) > 65536 {
		p = p[len(p)-65536:]
	}
	b.data = append(b.data, p...)
	if len(b.data) > 65536 {
		b.data = append([]byte(nil), b.data[len(b.data)-65536:]...)
	}
	return n, nil
}
func (b *adbOutputTail) text() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return strings.TrimSpace(string(b.data))
}
