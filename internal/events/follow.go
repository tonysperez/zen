package events

import (
	"bytes"
	"context"
	"io"
	"os"
	"time"
)

// Follow tails the log at path from offset, calling fn with each new
// complete line, until ctx is cancelled. It survives rotation: when path is
// replaced (Append renamed it to path+".1") or truncated, Follow finishes the
// old file and continues from the start of the new one. A log that does not
// exist yet is waited for.
func Follow(ctx context.Context, path string, offset int64, interval time.Duration, fn func(line []byte)) error {
	var (
		f       *os.File
		pending []byte
		buf     = make([]byte, 64*1024)
	)
	defer func() {
		if f != nil {
			f.Close()
		}
	}()

	// drain reads everything currently in f past offset and hands complete
	// lines to fn, keeping a trailing partial line in pending.
	drain := func() error {
		for {
			n, err := f.Read(buf)
			if n > 0 {
				offset += int64(n)
				pending = append(pending, buf[:n]...)
				for {
					i := bytes.IndexByte(pending, '\n')
					if i < 0 {
						break
					}
					if line := bytes.TrimSpace(pending[:i]); len(line) > 0 {
						fn(append([]byte(nil), line...))
					}
					pending = pending[i+1:]
				}
			}
			if err == io.EOF || n == 0 {
				return nil
			}
			if err != nil {
				return err
			}
		}
	}

	for {
		if f == nil {
			nf, err := os.Open(path)
			switch {
			case err == nil:
				if info, serr := nf.Stat(); serr == nil && info.Size() < offset {
					offset = 0 // rotated or truncated since offset was taken
				}
				if _, err := nf.Seek(offset, io.SeekStart); err != nil {
					nf.Close()
					return err
				}
				f = nf
			case !os.IsNotExist(err):
				return err
			}
		}

		if f != nil {
			if err := drain(); err != nil {
				return err
			}
			cur, statErr := os.Stat(path)
			open, _ := f.Stat()
			switch {
			case statErr != nil || open == nil || !os.SameFile(cur, open):
				// Rotated away: anything written before the rename is in
				// the old file, so read it once more before letting go.
				if err := drain(); err != nil {
					return err
				}
				f.Close()
				f, offset, pending = nil, 0, nil
				continue
			case cur.Size() < offset:
				if _, err := f.Seek(0, io.SeekStart); err != nil {
					return err
				}
				offset, pending = 0, nil
				continue
			}
		}

		select {
		case <-ctx.Done():
			return nil
		case <-time.After(interval):
		}
	}
}
