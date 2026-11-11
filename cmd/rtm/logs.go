package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"time"

	"github.com/HOYUN-Y/launchd-routine-manager/internal/logs"
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
	r, err := lookup(pos[0], env)
	if err != nil {
		return err
	}
	paths := r.LogPaths()
	if len(paths) == 0 {
		return fmt.Errorf("%s has no log path (no StandardOutPath/StandardErrorPath)", r.Label)
	}
	files := make([]followed, len(paths))
	for i, p := range paths {
		files[i].path = p
		if len(paths) > 1 {
			fmt.Fprintf(out, "==> %s <==\n", p)
		}
		lines, fi, err := logs.Tail(p, *n)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			fmt.Fprintf(out, "(log file does not exist yet: %s)\n", p)
		case err != nil:
			return err
		default:
			for _, l := range lines {
				fmt.Fprintln(out, l)
			}
			files[i].info, files[i].offset = fi, fi.Size()
		}
	}
	if *follow {
		followFiles(ctx, out, files)
	}
	return nil
}

// followed is one log file being followed: where we are in it, and which file it was.
type followed struct {
	path   string
	offset int64
	info   os.FileInfo // nil until the file has been seen
}

// followFiles prints whatever is appended to the files until ctx ends. A file that was
// replaced (log rotation: rename + new file, so os.SameFile is false) or that shrank
// (truncated) is read again from the start; comparing sizes alone misses a replacement whose
// new file is already longer than the old offset.
func followFiles(ctx context.Context, out io.Writer, files []followed) {
	t := time.NewTicker(followEvery)
	defer t.Stop()
	lastShown := -1
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		for i := range files {
			f := &files[i]
			cur, err := os.Stat(f.path)
			if err != nil {
				continue
			}
			if (f.info != nil && !os.SameFile(f.info, cur)) || cur.Size() < f.offset {
				f.offset = 0
			}
			f.info = cur
			if cur.Size() == f.offset {
				continue
			}
			fh, err := os.Open(f.path)
			if err != nil {
				continue
			}
			data, _ := io.ReadAll(io.NewSectionReader(fh, f.offset, cur.Size()-f.offset))
			fh.Close()
			if len(files) > 1 && lastShown != i {
				fmt.Fprintf(out, "==> %s <==\n", f.path)
				lastShown = i
			}
			out.Write(data)
			f.offset += int64(len(data))
		}
	}
}
