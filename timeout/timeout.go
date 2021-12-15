// Package timeout provides support for contexts with extendable timeouts.
package timeout

import (
	"context"
	"errors"
	"io"
	"time"
)

var (
	// errInvalidWrite means that a write returned an impossible count.
	errInvalidWrite = errors.New("timeout: invalid write result")
)

// Handler corresponds to a context created by the New function. It provides
// utility methods for extending the underlying context timeout during
// operations.
type Handler struct {
	cancel context.CancelFunc
	d      time.Duration
	timer  *time.Timer
}

// Cancel stops the underlying timer for the timeout, and cancels the associated
// context.
func (h *Handler) Cancel() {
	if h.timer.Stop() {
		h.cancel()
	}
}

// ExpireAfter resets the context to timeout after duration d.
func (h *Handler) ExpireAfter(d time.Duration) {
	h.timer.Reset(d)
}

// Copy copies from src to dst until either EOF is reached on src or an error
// occurs. It returns the number of bytes copied and the first error encountered
// while copying, if any.
//
// After each successful read, it keeps extending the timeout by the given
// duration d.
func (h *Handler) Copy(dst io.Writer, src io.Reader, buf []byte, d time.Duration) (written int64, err error) {
	if buf == nil {
		buf = make([]byte, 64*1024)
	}
	for {
		nr, er := src.Read(buf)
		if er == nil {
			h.ExpireAfter(d)
		}
		if nr > 0 {
			nw, ew := dst.Write(buf[0:nr])
			if nw < 0 || nr < nw {
				nw = 0
				if ew == nil {
					ew = errInvalidWrite
				}
			}
			written += int64(nw)
			if ew != nil {
				err = ew
				break
			}
			if nr != nw {
				err = io.ErrShortWrite
				break
			}
		}
		if er != nil {
			if er != io.EOF {
				err = er
			}
			break
		}
	}
	return written, err
}

// ReadAll reads from r until an error or EOF and returns the data it read. A
// successful call returns err == nil, not err == EOF. Because ReadAll is
// defined to read from src until EOF, it does not treat an EOF from Read as an
// error to be reported.
//
// After each successful read, it keeps extending the timeout by the given
// duration d.
func (h *Handler) ReadAll(r io.Reader, buf []byte, d time.Duration) ([]byte, error) {
	if buf == nil {
		buf = make([]byte, 64*1024)
	}
	buf = buf[:0]
	for {
		if len(buf) == cap(buf) {
			buf = append(buf, 0)[:len(buf)]
		}
		n, err := r.Read(buf[len(buf):cap(buf)])
		buf = buf[:len(buf)+n]
		if err != nil {
			if err == io.EOF {
				err = nil
			}
			return buf, err
		}
		h.ExpireAfter(d)
	}
}

// Reset resets the context timeout to the current moment.
func (h *Handler) Reset() {
	h.timer.Reset(h.d)
}

// New returns a new context that is automatically cancelled after the given
// timeout duration. The timeout duration can be extended using the Reset and
// ResetFor methods on the returned Handler.
func New(parent context.Context, d time.Duration) (context.Context, *Handler) {
	ctx, cancel := context.WithCancel(parent)
	timer := time.AfterFunc(d, cancel)
	return ctx, &Handler{
		cancel: cancel,
		d:      d,
		timer:  timer,
	}
}
