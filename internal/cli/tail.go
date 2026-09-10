package cli

import (
	"bytes"
	"context"
	"io"
	"os"
	"time"
)

// printLastLines writes the last n lines of a file to w.
func printLastLines(path string, n int, w io.Writer) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	lines := bytes.Split(bytes.TrimRight(data, "\n"), []byte("\n"))
	if n > 0 && len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	for _, l := range lines {
		_, _ = w.Write(l)
		_, _ = w.Write([]byte("\n"))
	}
	return nil
}

// followFile tails whatever path pathFn currently returns, printing appended
// bytes until ctx is cancelled. pathFn is re-evaluated so a daily rollover is
// followed automatically.
func followFile(ctx context.Context, pathFn func() string, w io.Writer) error {
	var (
		cur    string
		f      *os.File
		offset int64
	)
	defer func() {
		if f != nil {
			_ = f.Close()
		}
	}()

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			p := pathFn()
			if p != cur {
				if f != nil {
					_ = f.Close()
					f = nil
				}
				cur = p
				nf, err := os.Open(p)
				if err != nil {
					offset = 0
					continue
				}
				f = nf
				if info, err := f.Stat(); err == nil {
					offset = info.Size()
				}
				continue
			}
			if f == nil {
				continue
			}
			info, err := f.Stat()
			if err != nil {
				continue
			}
			if info.Size() <= offset {
				continue
			}
			if _, err := f.Seek(offset, io.SeekStart); err != nil {
				continue
			}
			n, _ := io.Copy(w, f)
			offset += n
			if fl, ok := w.(interface{ Flush() }); ok {
				fl.Flush()
			}
		}
	}
}
