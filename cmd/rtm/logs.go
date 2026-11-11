package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"
	"time"

	"github.com/HOYUN-Y/launchd-routine-manager/internal/sys"
)

var followEvery = 500 * time.Millisecond // tests shorten it

func cmdLogs(ctx context.Context, args []string, out, errw io.Writer, env sys.Env) error {
	fset := flag.NewFlagSet("logs", flag.ContinueOnError)
	fset.SetOutput(errw)
	n := fset.Int("n", 50, "number of lines")
	follow := fset.Bool("f", false, "keep following the files")
	pos, err := parse(fset, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 || *n < 0 {
		return fmt.Errorf("usage: rtm logs <label> [-n 50] [-f]")
	}
	r, err := lookup(pos[0], errw, env)
	if err != nil {
		return err
	}
	paths := r.LogPaths()
	if len(paths) == 0 {
		return fmt.Errorf("%s has no log path (no StandardOutPath/StandardErrorPath)", r.Label)
	}
	offsets := make([]int64, len(paths))
	for i, p := range paths {
		if len(paths) > 1 {
			fmt.Fprintf(out, "==> %s <==\n", p)
		}
		lines, size, err := tail(p, *n)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			fmt.Fprintf(out, "(log file does not exist yet: %s)\n", p)
		case err != nil:
			return err
		default:
			for _, l := range lines {
				fmt.Fprintln(out, l)
			}
			offsets[i] = size
		}
	}
	if *follow {
		followFiles(ctx, out, paths, offsets)
	}
	return nil
}

// tail returns the last n lines and the file size it read up to, reading from the end so a
// huge log is not loaded whole.
func tail(path string, n int) (lines []string, size int64, err error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, 0, err
	}
	size = fi.Size()
	if size == 0 || n == 0 {
		return nil, size, nil
	}
	for chunk := int64(64 << 10); ; chunk *= 2 {
		start := max(0, size-chunk)
		buf := make([]byte, size-start)
		if _, err := f.ReadAt(buf, start); err != nil && err != io.EOF {
			return nil, 0, err
		}
		if start > 0 && bytes.Count(buf, []byte("\n")) <= n {
			continue // not enough complete lines yet
		}
		lines = strings.Split(strings.TrimRight(string(buf), "\n"), "\n")
		if start > 0 {
			lines = lines[1:] // first line is cut in the middle
		}
		return lines[max(0, len(lines)-n):], size, nil
	}
}

// followFiles prints whatever is appended to the files until ctx ends. A file that shrinks
// (rotated or truncated) is re-read from the start.
func followFiles(ctx context.Context, out io.Writer, paths []string, offsets []int64) {
	t := time.NewTicker(followEvery)
	defer t.Stop()
	lastShown := -1
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		for i, p := range paths {
			fi, err := os.Stat(p)
			if err != nil {
				continue
			}
			if fi.Size() < offsets[i] {
				offsets[i] = 0
			}
			if fi.Size() == offsets[i] {
				continue
			}
			f, err := os.Open(p)
			if err != nil {
				continue
			}
			data, _ := io.ReadAll(io.NewSectionReader(f, offsets[i], fi.Size()-offsets[i]))
			f.Close()
			if len(paths) > 1 && lastShown != i {
				fmt.Fprintf(out, "==> %s <==\n", p)
				lastShown = i
			}
			out.Write(data)
			offsets[i] += int64(len(data))
		}
	}
}
