package models

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"slices"
	"strings"

	"github.com/sifty-works/sifty-server/internal/config"
)

//go:embed all:tasks
var taskFiles embed.FS

type Task struct {
	Name   string
	Work   string
	Prompt string
	Schema json.RawMessage
}

func loadTasks(files fs.FS) (map[string]Task, error) {
	dirs, err := fs.ReadDir(files, "tasks")
	if err != nil {
		return nil, err
	}
	tasks := map[string]Task{}
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		prompt, err := fs.ReadFile(files, "tasks/"+d.Name()+"/prompt.md")
		if err != nil {
			return nil, err
		}
		schema, err := fs.ReadFile(files, "tasks/"+d.Name()+"/schema.json")
		if err != nil {
			return nil, err
		}
		work, err := fs.ReadFile(files, "tasks/"+d.Name()+"/work.txt")
		if err != nil {
			return nil, err
		}
		kind := strings.TrimSpace(string(work))
		if !slices.Contains(config.WorkKinds, kind) {
			return nil, fmt.Errorf("task %s: unknown kind of work %q", d.Name(), kind)
		}
		tasks[d.Name()] = Task{Name: d.Name(), Work: kind, Prompt: string(prompt), Schema: schema}
	}
	return tasks, nil
}
