package tools

import (
	"bytes"
	"context"
	"strings"
	"unicode/utf8"

	"github.com/Sheyiyuan/half-pi/modules/half-pi-core/executor"
)

const commandProgressChunkBytes = 4 << 10

type commandOutput struct {
	ctx     context.Context
	kind    string
	all     bytes.Buffer
	pending []byte
	limit   int64
}

func (w *commandOutput) Write(data []byte) (int, error) {
	if w.limit == 0 {
		w.limit = executor.FinalOutputLimit(w.ctx)
	}
	if w.limit <= 0 || int64(w.all.Len()) < w.limit {
		retained := data
		if w.limit > 0 && int64(len(retained)) > w.limit-int64(w.all.Len()) {
			retained = retained[:w.limit-int64(w.all.Len())]
		}
		_, _ = w.all.Write(retained)
	}
	w.pending = append(w.pending, data...)
	w.emit(false)
	return len(data), nil
}

func (w *commandOutput) Flush() {
	w.emit(true)
}

func (w *commandOutput) String() string {
	return w.all.String()
}

func (w *commandOutput) Len() int {
	return w.all.Len()
}

func (w *commandOutput) emit(flush bool) {
	for len(w.pending) > 0 {
		var chunk strings.Builder
		consumed := 0
		for consumed < len(w.pending) {
			remaining := w.pending[consumed:]
			if !flush && !utf8.FullRune(remaining) {
				break
			}
			r, size := utf8.DecodeRune(remaining)
			if r == utf8.RuneError && size == 1 {
				r = utf8.RuneError
			}
			if chunk.Len()+utf8.RuneLen(r) > commandProgressChunkBytes {
				break
			}
			chunk.WriteRune(r)
			consumed += size
		}
		if consumed == 0 {
			return
		}
		executor.ReportProgress(w.ctx, executor.Progress{Kind: w.kind, Data: chunk.String()})
		w.pending = w.pending[consumed:]
	}
}
