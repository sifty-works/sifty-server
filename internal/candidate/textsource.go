package candidate

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/sifty-works/sifty-server/internal/models"
)

type TextSource interface {
	Text(ctx context.Context, body []byte) (string, error)
}

const (
	defaultTextTimeout   = 30 * time.Second
	defaultTextMaxOutput = 4 << 20
)

var ErrProgramMissing = errors.New("the program is not installed")

type ProgramText struct {
	Program   string
	Args      []string
	Timeout   time.Duration
	MaxOutput int
}

func (p ProgramText) Text(ctx context.Context, body []byte) (string, error) {
	timeout, maxOutput := p.Timeout, p.MaxOutput
	if timeout <= 0 {
		timeout = defaultTextTimeout
	}
	if maxOutput <= 0 {
		maxOutput = defaultTextMaxOutput
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	out, err := models.RunCLI(ctx, p.Program, p.Args, string(body), models.IsolatedEnv(), maxOutput)
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) || errors.Is(err, fs.ErrNotExist) {
			return "", ErrProgramMissing
		}
		return "", err
	}
	return strings.TrimSpace(strings.ReplaceAll(out, "\f", "\n")), nil
}

func pdfInstallReason() string {
	switch runtime.GOOS {
	case "darwin":
		return "PDF text needs poppler on this machine. Install it with: brew install poppler"
	case "linux":
		return "PDF text needs poppler on this machine. Install it with: sudo apt install poppler-utils"
	}
	return "PDF text needs poppler (the pdftotext program) on this machine. Install it with your package manager."
}

func extractPDF(ctx context.Context, src TextSource, logger *slog.Logger, body []byte) (text, state, stateReason string) {
	t, err := src.Text(ctx, body)
	if err != nil && !errors.Is(err, ErrProgramMissing) {
		logger.Warn("pdf text", "err", err)
	}
	switch {
	case errors.Is(err, ErrProgramMissing):
		return "", "unavailable", pdfInstallReason()
	case errors.Is(err, context.DeadlineExceeded):
		return "", "error", "Reading the PDF took too long and was stopped. Refresh to try again."
	case errors.Is(err, models.ErrOutputTooLong):
		return "", "error", "The text of the PDF is too long to keep."
	case err != nil:
		return "", "error", "The PDF could not be read."
	case t == "":
		return "", "error", "No text was found in the PDF. It may be a scan of a page."
	}
	return t, "ready", ""
}
