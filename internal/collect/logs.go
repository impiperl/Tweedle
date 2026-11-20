package collect

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// LogLine is one line of container output as podman prints it with
// --timestamps; Time is zero if the line had no readable timestamp.
type LogLine struct {
	Time time.Time
	Text string
}

// MaxLogLineLen is where a single log line is cut off (in bytes).
const MaxLogLineLen = 4000

// ContainerLogs runs "<cmd> logs --timestamps [--since T] --tail N NAME" and
// returns its lines (stdout and stderr in the order podman wrote them).
func ContainerLogs(cmd []string, name string, since time.Time, tail int) ([]LogLine, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	args := append([]string{}, cmd[1:]...)
	args = append(args, "logs", "--timestamps", "--tail", strconv.Itoa(tail))
	if !since.IsZero() {
		args = append(args, "--since", since.UTC().Format(time.RFC3339))
	}
	args = append(args, "--", name)
	c := exec.CommandContext(ctx, cmd[0], args...)
	pr, pw := io.Pipe()
	// The same writer for both keeps the order of stdout and stderr.
	c.Stdout, c.Stderr = pw, pw
	if err := c.Start(); err != nil {
		return nil, err
	}
	waitErr := make(chan error, 1)
	go func() {
		err := c.Wait()
		pw.Close()
		waitErr <- err
	}()
	lines, readErr := readLogLines(pr, tail)
	pr.Close()
	err := <-waitErr
	if ctx.Err() != nil {
		return nil, fmt.Errorf("podman logs: timeout")
	}
	if err != nil {
		// podman's own message is the last line it printed.
		msg := err.Error()
		if n := len(lines); n > 0 && lines[n-1].Time.IsZero() {
			msg = strings.TrimSpace(lines[n-1].Text)
		}
		return nil, fmt.Errorf("%s", msg)
	}
	if readErr != nil {
		return nil, readErr
	}
	return lines, nil
}

// readLogLines reads at most max lines (a few extra for podman messages),
// cutting long lines at MaxLogLineLen.
func readLogLines(r io.Reader, max int) ([]LogLine, error) {
	br := bufio.NewReaderSize(r, 64<<10)
	var out []LogLine
	var cur []byte
	cut := false
	for {
		frag, isPrefix, err := br.ReadLine()
		if len(cur) < MaxLogLineLen {
			room := MaxLogLineLen - len(cur)
			if len(frag) > room {
				frag, cut = frag[:room], true
			}
			cur = append(cur, frag...)
		} else if len(frag) > 0 {
			cut = true
		}
		if err == nil && isPrefix {
			continue
		}
		if err == nil || len(cur) > 0 {
			text := string(cur)
			if cut {
				text += " …"
			}
			if len(out) < max+5 {
				out = append(out, parseLogLine(text))
			}
		}
		cur, cut = cur[:0], false
		if errors.Is(err, io.EOF) {
			return out, nil
		}
		if err != nil {
			return out, err
		}
	}
}

// parseLogLine splits off the timestamp podman puts in front with --timestamps.
func parseLogLine(s string) LogLine {
	if ts, rest, ok := strings.Cut(s, " "); ok && len(ts) >= 20 && ts[4] == '-' && ts[10] == 'T' {
		if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
			return LogLine{Time: t, Text: rest}
		}
	}
	return LogLine{Text: s}
}
