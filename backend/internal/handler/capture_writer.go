package handler

import (
	"bufio"
	"bytes"
	"errors"
	"net"
	"net/http"

	"github.com/gin-gonic/gin"
)

type captureWriter struct {
	base   gin.ResponseWriter
	header http.Header
	status int
	size   int
	buf    bytes.Buffer
}

func newCaptureWriter(base gin.ResponseWriter) *captureWriter {
	return &captureWriter{
		base:   base,
		header: make(http.Header),
		status: http.StatusOK,
	}
}

func (w *captureWriter) Header() http.Header { return w.header }

func (w *captureWriter) WriteHeader(code int) { w.status = code }

func (w *captureWriter) WriteHeaderNow() {}

func (w *captureWriter) Write(p []byte) (int, error) {
	n, err := w.buf.Write(p)
	w.size += n
	return n, err
}

func (w *captureWriter) WriteString(s string) (int, error) {
	n, err := w.buf.WriteString(s)
	w.size += n
	return n, err
}

func (w *captureWriter) Status() int { return w.status }

func (w *captureWriter) Size() int { return w.size }

func (w *captureWriter) Written() bool { return w.size > 0 }

func (w *captureWriter) Flush() {}

func (w *captureWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if hj, ok := w.base.(http.Hijacker); ok {
		return hj.Hijack()
	}
	return nil, nil, errors.New("hijack not supported")
}

func (w *captureWriter) CloseNotify() <-chan bool {
	if cn, ok := w.base.(http.CloseNotifier); ok {
		return cn.CloseNotify()
	}
	ch := make(chan bool)
	close(ch)
	return ch
}

func (w *captureWriter) Pusher() http.Pusher {
	if p, ok := w.base.(interface{ Pusher() http.Pusher }); ok {
		return p.Pusher()
	}
	return nil
}
