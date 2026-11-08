package downloader

import (
	"context"
	"fmt"
	"io"
	"sync/atomic"
	"time"
)

type downloadWatch struct {
	ctx     context.Context
	cancel  context.CancelFunc
	timer   *time.Timer
	expired atomic.Bool
	timeout time.Duration
}

func newDownloadWatch(parent context.Context, timeout time.Duration) *downloadWatch {
	ctx, cancel := context.WithCancel(parent)
	w := &downloadWatch{ctx: ctx, cancel: cancel, timeout: timeout}
	w.timer = time.AfterFunc(timeout, func() { w.expired.Store(true); cancel() })
	return w
}
func (w *downloadWatch) close() { w.timer.Stop(); w.cancel() }
func (w *downloadWatch) reason(err error) error {
	if w.expired.Load() {
		return fmt.Errorf("下载连续 %s 无数据，已停止等待：%w", w.timeout, err)
	}
	return err
}

type downloadIdleBody struct {
	io.ReadCloser
	watch *downloadWatch
}

func (b *downloadIdleBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if n > 0 {
		b.watch.timer.Reset(b.watch.timeout)
	}
	if err != nil {
		return n, b.watch.reason(err)
	}
	return n, nil
}
