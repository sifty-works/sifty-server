package answer

import (
	"log/slog"
	"net/http"

	"github.com/sifty-works/sifty-server/internal/server"
)

type FillOption struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

type FillField struct {
	Key      string       `json:"key"`
	Label    string       `json:"label"`
	Kind     string       `json:"kind"`
	Required bool         `json:"required"`
	Format   string       `json:"format,omitempty"`
	Options  []FillOption `json:"options"`
	Value    string       `json:"value,omitempty"`
	Values   []string     `json:"values,omitempty"`
}

type FillRequest struct {
	URL    string      `json:"url"`
	Title  string      `json:"title"`
	Text   string      `json:"text"`
	Fields []FillField `json:"fields"`
}

type FillVacancy struct {
	ID      int64  `json:"id"`
	Title   string `json:"title"`
	Company string `json:"company"`
}

type FillAnswer struct {
	Key    string   `json:"key"`
	Value  string   `json:"value,omitempty"`
	Values []string `json:"values,omitempty"`
	Source string   `json:"source"`
	Note   string   `json:"note"`
}

type FillLeft struct {
	Key    string `json:"key"`
	Reason string `json:"reason"`
}

type FillResponse struct {
	Form    int64        `json:"form"`
	Vacancy *FillVacancy `json:"vacancy"`
	State   string       `json:"state"`
	Answers []FillAnswer `json:"answers"`
	Left    []FillLeft   `json:"left"`
	Open    string       `json:"open"`
}

const notReady = "Sifty cannot answer forms from the browser extension yet."

func RegisterExtensionRoutes(mux *http.ServeMux, logger *slog.Logger) {
	server.Handle(mux, "POST /api/extension/fill", logger, func(_ *http.Request, _ FillRequest) (FillResponse, error) {
		return FillResponse{}, server.NotImplemented{Message: notReady}
	})
	server.Handle(mux, "GET /api/extension/fill/{form}", logger, func(_ *http.Request, _ struct{}) (FillResponse, error) {
		return FillResponse{}, server.NotImplemented{Message: notReady}
	})
}
