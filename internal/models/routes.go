package models

import (
	"context"
	"fmt"
	"maps"
	"net/http"
	"os"
	"slices"
	"strings"

	"github.com/sifty-works/sifty-server/internal/apperr"
	"github.com/sifty-works/sifty-server/internal/config"
)

type checkResp struct {
	State string `json:"state"`
}

type modelsPatch struct {
	Work           map[string]string `json:"work,omitempty"`
	AllowPaidBulk  *bool             `json:"allowPaidBulk,omitempty"`
	Paused         *bool             `json:"paused,omitempty"`
	DailyLimit     *int              `json:"dailyLimit,omitempty"`
	PreciseReading *bool             `json:"preciseReading,omitempty"`
}

type providerView struct {
	Name  string `json:"name"`
	Kind  string `json:"kind"`
	Model string `json:"model"`
	Cost  string `json:"cost"`
	State string `json:"state"`
}

type workView struct {
	Kind     string   `json:"kind"`
	Provider string   `json:"provider"`
	Choices  []string `json:"choices"`
}

type modelsResp struct {
	Providers      []providerView `json:"providers"`
	Work           []workView     `json:"work"`
	AllowPaidBulk  bool           `json:"allowPaidBulk"`
	Paused         bool           `json:"paused"`
	DailyLimit     int            `json:"dailyLimit"`
	TodayCalls     int            `json:"todayCalls"`
	PreciseReading bool           `json:"preciseReading"`
}

func getModels(ctx context.Context, gate *Gate) modelsResp {
	out := modelsResp{Providers: []providerView{}, Work: []workView{}}
	if gate == nil {
		return out
	}
	cfg := gate.Store().Get()
	status := gate.Status()
	names := slices.Sorted(maps.Keys(cfg.Providers))
	if names == nil {
		names = []string{}
	}
	for _, name := range names {
		pc := cfg.Providers[name]
		out.Providers = append(out.Providers, providerView{
			Name: name, Kind: pc.Type, Model: pc.Model, Cost: pc.Cost,
			State: status.Providers[name].State,
		})
	}
	for _, kind := range config.WorkKinds {
		out.Work = append(out.Work, workView{Kind: kind, Provider: cfg.WorkProviderFor(kind), Choices: names})
	}
	out.AllowPaidBulk = cfg.AllowPaidBulk
	out.Paused = status.Paused
	out.DailyLimit = cfg.DailyModelLimit
	out.PreciseReading = cfg.PreciseReadingOn()
	out.TodayCalls = gate.TodayBackgroundCalls(ctx)
	return out
}

func patchModels(ctx context.Context, gate *Gate, req modelsPatch, changed func()) (modelsResp, error) {
	if gate == nil {
		return getModels(ctx, gate), nil
	}
	if req.DailyLimit != nil && *req.DailyLimit < 0 {
		return modelsResp{}, apperr.Invalid{Message: "The daily limit of background model calls is 0 or more; 0 means no limit."}
	}
	if req.Work != nil || req.AllowPaidBulk != nil || req.DailyLimit != nil || req.PreciseReading != nil {
		err := gate.Store().Update(func(c *config.Config) error {
			for kind, provider := range req.Work {
				if !c.SetWorkProvider(kind, provider) {
					return apperr.Invalid{Message: fmt.Sprintf("There is no kind of model work called %s. A kind of model work is one of %s.", kind, config.JoinOr(config.WorkKinds))}
				}
			}
			if req.AllowPaidBulk != nil {
				c.AllowPaidBulk = *req.AllowPaidBulk
			}
			if req.DailyLimit != nil {
				c.DailyModelLimit = *req.DailyLimit
			}
			if req.PreciseReading != nil {
				c.PreciseReading = req.PreciseReading
			}
			return nil
		})
		if err != nil {
			return modelsResp{}, err
		}
		if changed != nil && (req.Work != nil || req.PreciseReading != nil || req.AllowPaidBulk != nil) {
			changed()
		}
	}
	if req.Paused != nil {
		if err := gate.SetPaused(ctx, *req.Paused); err != nil {
			return modelsResp{}, err
		}
	}
	return getModels(ctx, gate), nil
}

func checkProvider(ctx context.Context, gate *Gate, name string) checkResp {
	if gate == nil {
		return checkResp{State: "Offline"}
	}
	state, _ := gate.Check(ctx, name)
	if state == "" {
		state = "Offline"
	}
	return checkResp{State: state}
}

func CheckProviderHandler(gate *Gate) func(*http.Request, struct{}) (checkResp, error) {
	return func(r *http.Request, _ struct{}) (checkResp, error) {
		return checkProvider(r.Context(), gate, r.PathValue("name")), nil
	}
}

func ModelsHandler(gate *Gate) func(*http.Request, struct{}) (modelsResp, error) {
	return func(r *http.Request, _ struct{}) (modelsResp, error) {
		return getModels(r.Context(), gate), nil
	}
}

func PatchModelsHandler(gate *Gate, changed func()) func(*http.Request, modelsPatch) (modelsResp, error) {
	return func(r *http.Request, req modelsPatch) (modelsResp, error) {
		return patchModels(r.Context(), gate, req, changed)
	}
}

type secretView struct {
	Name      string   `json:"name"`
	Providers []string `json:"providers"`
	Set       bool     `json:"set"`
	Last4     string   `json:"last4"`
	Source    string   `json:"source"`
}

type secretsResp struct {
	Keys []secretView `json:"keys"`
}

type secretPut struct {
	Value string `json:"value"`
}

const minShownLast4 = 8

func getSecrets(gate *Gate) secretsResp {
	out := secretsResp{Keys: []secretView{}}
	if gate == nil {
		return out
	}
	users := map[string][]string{}
	for provider, pc := range gate.Store().Get().Providers {
		if pc.KeyEnv != "" {
			users[pc.KeyEnv] = append(users[pc.KeyEnv], provider)
		}
	}
	for _, name := range slices.Sorted(maps.Keys(users)) {
		v := secretView{Name: name, Providers: slices.Sorted(slices.Values(users[name]))}
		value := os.Getenv(name)
		switch {
		case value != "":
			v.Source = "environment"
		case gate.Secrets().Get(name) != "":
			value, v.Source = gate.Secrets().Get(name), "file"
		}
		v.Set = value != ""
		if len(value) >= minShownLast4 {
			v.Last4 = value[len(value)-4:]
		}
		out.Keys = append(out.Keys, v)
	}
	return out
}

func changeSecret(gate *Gate, name string, value *string) (secretsResp, error) {
	if gate == nil || !slices.ContainsFunc(getSecrets(gate).Keys, func(k secretView) bool { return k.Name == name }) {
		return secretsResp{}, apperr.Invalid{Message: fmt.Sprintf("No provider uses a key called %s.", name)}
	}
	if os.Getenv(name) != "" {
		return secretsResp{}, apperr.Conflict{Message: "This key comes from the environment; change it there."}
	}
	var err error
	if value == nil {
		err = gate.Secrets().Remove(name)
	} else {
		err = gate.Secrets().Set(name, *value)
	}
	if err != nil {
		return secretsResp{}, err
	}
	gate.forgetKey(name)
	return getSecrets(gate), nil
}

func SecretsHandler(gate *Gate) func(*http.Request, struct{}) (secretsResp, error) {
	return func(*http.Request, struct{}) (secretsResp, error) { return getSecrets(gate), nil }
}

func PutSecretHandler(gate *Gate) func(*http.Request, secretPut) (secretsResp, error) {
	return func(r *http.Request, req secretPut) (secretsResp, error) {
		value := strings.TrimSpace(req.Value)
		switch {
		case strings.ContainsAny(value, "\r\n"):
			return secretsResp{}, apperr.Invalid{Message: "A key is one line."}
		case value == "":
			return secretsResp{}, apperr.Invalid{Message: "Enter a key, or remove the saved one."}
		}
		return changeSecret(gate, r.PathValue("name"), &value)
	}
}

func DeleteSecretHandler(gate *Gate) func(*http.Request, struct{}) (secretsResp, error) {
	return func(r *http.Request, _ struct{}) (secretsResp, error) {
		return changeSecret(gate, r.PathValue("name"), nil)
	}
}
