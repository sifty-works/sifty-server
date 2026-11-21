package models

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

var fenceRe = regexp.MustCompile("(?s)^```(?:json)?\\s*|\\s*```$")

func unwrapFence(text string) string {
	return strings.TrimSpace(fenceRe.ReplaceAllString(strings.TrimSpace(text), ""))
}

func IsolatedEnv() []string {
	keys := []string{"PATH", "HOME", "USER", "LOGNAME", "SHELL", "TMPDIR", "LANG", "TERM",
		"XDG_DATA_HOME", "XDG_CACHE_HOME"}
	var env []string
	for _, k := range keys {
		if v, ok := os.LookupEnv(k); ok {
			env = append(env, k+"="+v)
		}
	}
	return env
}

const maxModelCLIOutput = 8 << 20

var ErrOutputTooLong = errors.New("output is longer than the limit")

type capWriter struct {
	buf    bytes.Buffer
	limit  int
	over   bool
	cancel context.CancelFunc
}

func (w *capWriter) Write(p []byte) (int, error) {
	if w.buf.Len()+len(p) > w.limit {
		w.over = true
		w.cancel()
		return 0, ErrOutputTooLong
	}
	return w.buf.Write(p)
}

func RunCLI(ctx context.Context, name string, args []string, stdin string, env []string, maxOutput int) (string, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = env
	cmd.Stdin = strings.NewReader(stdin)
	cmd.WaitDelay = 2 * time.Second
	out := &capWriter{limit: maxOutput, cancel: cancel}
	var errBuf bytes.Buffer
	cmd.Stdout = out
	cmd.Stderr = &errBuf
	err := cmd.Run()
	if out.over {
		return "", fmt.Errorf("%s: %w (%d bytes)", name, ErrOutputTooLong, maxOutput)
	}
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", fmt.Errorf("%s: %w (%s)", name, err, strings.TrimSpace(errBuf.String()))
	}
	return out.buf.String(), nil
}
