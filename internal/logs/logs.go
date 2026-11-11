// Package logs reads log files for rtm.
package logs

import (
	"bytes"
	"io"
	"os"
	"strings"
)

// Tail returns the last n lines and the file's info as it was when read (Size() is how far
// it read), reading from the end so a huge log is not loaded whole.
func Tail(path string, n int) (lines []string, fi os.FileInfo, err error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	fi, err = f.Stat()
	if err != nil {
		return nil, nil, err
	}
	size := fi.Size()
	if size == 0 || n == 0 {
		return nil, fi, nil
	}
	for chunk := int64(64 << 10); ; chunk *= 2 {
		start := max(0, size-chunk)
		buf := make([]byte, size-start)
		if _, err := f.ReadAt(buf, start); err != nil && err != io.EOF {
			return nil, nil, err
		}
		if start > 0 && bytes.Count(buf, []byte("\n")) <= n {
			continue // not enough complete lines yet
		}
		// Only the final newline ends the last line; blank lines before it are real lines.
		lines = strings.Split(strings.TrimSuffix(string(buf), "\n"), "\n")
		if start > 0 {
			lines = lines[1:] // first line is cut in the middle
		}
		return lines[max(0, len(lines)-n):], fi, nil
	}
}
