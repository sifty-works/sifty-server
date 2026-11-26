package models

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"reflect"
	"slices"
	"strings"

	"github.com/sifty-works/sifty-server/internal/config"
)

//go:embed all:tasks
var taskFiles embed.FS

var tasks = mustLoadTasks()

type Task struct {
	Name   string
	Work   string
	Item   string
	Prompt string
	Schema json.RawMessage
}

func NamedTask(name string) Task {
	t, ok := tasks[name]
	if !ok {
		panic("models: no task " + name)
	}
	return t
}

const Asks = 2

func (t Task) Ask(ctx context.Context, m Model, prompt string, attempts int, out any) (int, error) {
	var unreadable error
	for used := 1; used <= attempts; used++ {
		resp, err := m.Complete(ctx, Request{Prompt: prompt, Schema: t.Schema, Think: "off"})
		if err != nil {
			return used, Tell(err, t.Work, t.Item)
		}
		reflect.ValueOf(out).Elem().SetZero()
		if unreadable = json.Unmarshal([]byte(resp.Text), out); unreadable == nil {
			return used, nil
		}
	}
	return attempts, unreadableAnswer{fmt.Errorf("models: task %s: unreadable answer: %w", t.Name, unreadable)}
}

type unreadableAnswer struct{ error }

func (u unreadableAnswer) Unwrap() error { return u.error }

func (unreadableAnswer) Final() bool { return true }

func mustLoadTasks() map[string]Task {
	loaded, err := loadTasks(taskFiles)
	if err != nil {
		panic(err)
	}
	return loaded
}

func loadTasks(files fs.FS) (map[string]Task, error) {
	dirs, err := fs.ReadDir(files, "tasks")
	if err != nil {
		return nil, err
	}
	loaded := map[string]Task{}
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		var text [4][]byte
		for i, file := range []string{"prompt.md", "schema.json", "work.txt", "item.txt"} {
			if text[i], err = fs.ReadFile(files, "tasks/"+d.Name()+"/"+file); err != nil {
				return nil, err
			}
		}
		kind := strings.TrimSpace(string(text[2]))
		if !slices.Contains(config.WorkKinds, kind) {
			return nil, fmt.Errorf("task %s: unknown kind of work %q", d.Name(), kind)
		}
		loaded[d.Name()] = Task{
			Name:   d.Name(),
			Work:   kind,
			Item:   strings.TrimSpace(string(text[3])),
			Prompt: string(text[0]),
			Schema: text[1],
		}
	}
	return loaded, nil
}
