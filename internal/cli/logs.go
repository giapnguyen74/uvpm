package cli

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/giapnguyen74/uvpm/internal/model"
)

type logSource struct {
	label, path string
}

func tailLines(path string, n int) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil || fi.Size() == 0 {
		return nil
	}
	size := int64(256 << 10)
	if fi.Size() < size {
		size = fi.Size()
	}
	buf := make([]byte, size)
	if _, err := f.ReadAt(buf, fi.Size()-size); err != nil && err != io.EOF {
		return nil
	}
	lines := bytes.Split(bytes.TrimRight(buf, "\n"), []byte("\n"))
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = string(l)
	}
	return out
}

func showLogs(apps []model.AppInfo, lines int, follow, onlyOut, onlyErr bool) {
	var srcs []logSource
	for _, a := range apps {
		if !onlyErr {
			srcs = append(srcs, logSource{a.Name, a.OutLog})
		}
		if !onlyOut {
			srcs = append(srcs, logSource{a.Name + " (err)", a.ErrLog})
		}
	}
	var mu sync.Mutex
	emit := func(label, line string) {
		mu.Lock()
		fmt.Printf("%s | %s\n", label, line)
		mu.Unlock()
	}
	for _, s := range srcs {
		for _, l := range tailLines(s.path, lines) {
			emit(s.label, l)
		}
	}
	if !follow {
		return
	}
	var wg sync.WaitGroup
	for _, s := range srcs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			followFile(s, emit)
		}()
	}
	wg.Wait()
}

// followFile polls a log file for appended lines; it copes with truncation.
func followFile(s logSource, emit func(label, line string)) {
	var off int64
	if fi, err := os.Stat(s.path); err == nil {
		off = fi.Size()
	}
	var partial []byte
	for {
		time.Sleep(200 * time.Millisecond)
		fi, err := os.Stat(s.path)
		if err != nil {
			continue
		}
		if fi.Size() < off {
			off, partial = 0, nil
		}
		if fi.Size() == off {
			continue
		}
		f, err := os.Open(s.path)
		if err != nil {
			continue
		}
		buf := make([]byte, fi.Size()-off)
		n, _ := f.ReadAt(buf, off)
		f.Close()
		off += int64(n)
		partial = append(partial, buf[:n]...)
		for {
			i := bytes.IndexByte(partial, '\n')
			if i < 0 {
				break
			}
			emit(s.label, string(partial[:i]))
			partial = partial[i+1:]
		}
	}
}
