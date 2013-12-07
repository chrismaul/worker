package main

import (
	"bufio"
	"io"
	"time"
)

type CoalesceWriteCloser struct {
	wc     io.WriteCloser
	buf    *bufio.Writer
	cancel chan bool
	write  chan writeReq
	err    error
}

type writeRes struct {
	n   int
	err error
}

type writeReq struct {
	buf []byte
	res chan writeRes
}

func NewCoalesceWriteCloser(wc io.WriteCloser) *CoalesceWriteCloser {
	c := &CoalesceWriteCloser{
		wc:     wc,
		buf:    bufio.NewWriterSize(wc, 9216),
		cancel: make(chan bool),
		write:  make(chan writeReq),
	}

	go func() {
		ticker := time.NewTicker(500 * time.Millisecond)
		for {
			select {
			case <-c.cancel:
				ticker.Stop()
				return
			case wreq := <-c.write:
				n, err := c.buf.Write(wreq.buf)
				wreq.res <- writeRes{n, err}
			case <-ticker.C:
				c.err = c.buf.Flush()
				if c.err != nil {
					ticker.Stop()
					return
				}
			}
		}
	}()

	return c
}

func (c *CoalesceWriteCloser) Write(p []byte) (int, error) {
	if c.err != nil {
		return 0, c.err
	}

	wreq := writeReq{p, make(chan writeRes)}
	c.write <- wreq
	wres := <-wreq.res
	return wres.n, wres.err
}

func (c *CoalesceWriteCloser) Close() error {
	c.cancel <- true
	if c.err != nil {
		c.wc.Close()
		return c.err
	}

	err := c.buf.Flush()
	if err != nil {
		c.wc.Close()
		return err
	}

	return c.wc.Close()
}

type TimeoutWriter struct {
	Timeout <-chan time.Time
	timer   *time.Timer
	w       io.WriteCloser
	d       time.Duration
}

func NewTimeoutWriter(w io.WriteCloser, d time.Duration) *TimeoutWriter {
	timer := time.NewTimer(d)

	return &TimeoutWriter{
		Timeout: timer.C,
		timer:   timer,
		w:       w,
		d:       d,
	}
}

func (tw *TimeoutWriter) Write(p []byte) (int, error) {
	tw.timer.Reset(tw.d)

	return tw.w.Write(p)
}

func (tw *TimeoutWriter) Close() error {
	tw.timer.Stop()

	return tw.w.Close()
}

type addReq struct {
	added int
	done  chan bool
}

type LimitWriter struct {
	w            io.WriteCloser
	n            int64
	LimitReached chan bool
	add          chan addReq
}

func NewLimitWriter(w io.WriteCloser, n int64) *LimitWriter {
	lw := &LimitWriter{w, n, make(chan bool, 1), make(chan addReq)}

	go func() {
		var n int64 = 0
		for {
			add := <-lw.add
			n += int64(add.added)
			if n > lw.n {
				lw.LimitReached <- true
				add.done <- true
				return
			}
			add.done <- true
		}
	}()

	return lw
}

func (lw *LimitWriter) Write(p []byte) (int, error) {
	done := make(chan bool)
	lw.add <- addReq{len(p), done}
	<-done
	return lw.w.Write(p)
}

func (lw *LimitWriter) Close() error {
	return lw.w.Close()
}
