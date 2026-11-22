package candidate

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/sifty-works/sifty-server/internal/apperr"
	"github.com/sifty-works/sifty-server/internal/fetch"
	"github.com/sifty-works/sifty-server/internal/listing"
)

func linkKey(typed string) (string, error) {
	typed = strings.TrimSpace(typed)
	if typed == "" {
		return "", apperr.Invalid{Message: "Enter a URL."}
	}
	key, err := listing.Canonical(typed)
	if err != nil {
		return "", apperr.Invalid{Message: "Enter a web address that starts with http:// or https://."}
	}
	return key, nil
}

func linkSource(_ string, row documentRow) ([]byte, error) {
	return []byte(row.Address), nil
}

func readLink(ctx context.Context, r Readers, logger *slog.Logger, address []byte) (string, string, string, error) {
	resp, err := r.Pages.Get(ctx, string(address), fetch.Opt{Accept: "text/html,application/xhtml+xml"})
	if err != nil {
		return "", "", "", linkError(err, logger)
	}
	kind := resp.ContentType
	if kind == "" {
		kind = http.DetectContentType(resp.Body)
	}
	if !strings.Contains(strings.ToLower(kind), "html") {
		return "", "", "", apperr.Invalid{Message: "The address does not lead to a web page."}
	}
	text := listing.PlainText(listing.Normalize(string(resp.Body)))
	if text == "" {
		return "", "", "", apperr.Invalid{Message: "The page has no text."}
	}
	return text, "ready", "", nil
}

func linkError(err error, logger *slog.Logger) error {
	switch {
	case errors.Is(err, fetch.ErrRefused):
		return apperr.Invalid{Message: "That address is private or not allowed, so Sifty will not open it."}
	case errors.Is(err, fetch.ErrBlocked):
		return apperr.Invalid{Message: "The page cannot be read automatically. It may need a login, or the site may refuse automatic reading."}
	case errors.Is(err, fetch.ErrNotFound):
		return apperr.Invalid{Message: "The page was not found."}
	case errors.Is(err, fetch.ErrOffline):
		return apperr.Invalid{Message: "Sifty is offline, so it cannot read pages."}
	}
	logger.Warn("link page", "err", err)
	return apperr.Invalid{Message: "The page could not be loaded. Try again later."}
}
