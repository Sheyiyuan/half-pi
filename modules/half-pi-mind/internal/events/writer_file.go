package events

import (
	"encoding/json"
	"os"
	"sync"
)

// FileWriter 将事件以 JSON Lines 格式写入文件。
// 每行一条 JSON 事件，便于后期导入日志分析系统。
type FileWriter struct {
	f  *os.File
	mu sync.Mutex
}

func NewFileWriter(path string) (*FileWriter, error) {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return nil, err
	}
	return &FileWriter{f: f}, nil
}

func (w *FileWriter) WriteEvent(e Event) error {
	line, err := json.Marshal(e)
	if err != nil {
		return err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	_, err = w.f.Write(append(line, '\n'))
	return err
}

func (w *FileWriter) Close() error {
	return w.f.Close()
}
