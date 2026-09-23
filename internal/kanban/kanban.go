// Package kanban reads and writes kanban tasks as .md files with YAML frontmatter.
// Files are stored in
// ~/.ai/automata/profiles/<profile>/domains/<domain>/kanban/<task-name>.md
//
// Format:
//
//	---
//	title: Task name
//	status: todo | pending | progress | done
//	assigned_to: ""  # session ID of the AI agent working on this task
//	substatus: ""    # read | write | find | grep | analyze | wait | thinking | job
//	---
//
// Body of the task (description)
package kanban

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/HumanHorizon/automata/internal/paths"
)

// ErrDoneToProgressForbidden is returned by UpdateStatus when a caller tries
// to move a task back from "done" to "progress". Once a task is done it is
// considered closed and the UI no longer renders a transition for it; this
// error guards against race conditions, scripted edits, or direct .md file
// rewrites that try to bypass the UI gate.
var ErrDoneToProgressForbidden = errors.New("kanban: task in done cannot be moved back to progress")

// Task represents a single kanban task.
type Task struct {
	Title       string            `yaml:"title"`
	Status      string            `yaml:"status"`
	AssignedTo  string            `yaml:"assigned_to"`
	Substatus   string            `yaml:"substatus"`
	Description string            // body of the .md file (after frontmatter)
	Path        string            // full path to the .md file
	Metadata    map[string]string // unknown frontmatter fields preserved on write
}

// ReadAll reads all kanban tasks for the given domain within the given profile.
func ReadAll(domain, profile string) ([]Task, error) {
	dir := KanbanDir(domain, profile)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read kanban dir %s: %w", dir, err)
	}

	var tasks []Task
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		task, err := readTask(path)
		if err != nil {
			continue
		}
		task.Path = path
		tasks = append(tasks, task)
	}

	sort.Slice(tasks, func(i, j int) bool {
		return tasks[i].Title < tasks[j].Title
	})

	return tasks, nil
}

// KanbanDir returns the kanban directory for the given profile+domain.
func KanbanDir(domain, profile string) string {
	return filepath.Join(paths.DomainDir(profile, domain), "kanban")
}

// readTask parses a single .md file with YAML frontmatter.
func readTask(path string) (Task, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Task{}, err
	}

	content := string(data)
	task := Task{Metadata: make(map[string]string)}

	if strings.HasPrefix(content, "---") {
		parts := strings.SplitN(content[3:], "---", 2)
		if len(parts) >= 2 {
			frontmatter := strings.TrimSpace(parts[0])
			for _, line := range strings.Split(frontmatter, "\n") {
				line = strings.TrimSpace(line)
				if line == "" {
					continue
				}
				colon := strings.IndexByte(line, ':')
				if colon < 0 {
					continue
				}
				key := strings.TrimSpace(line[:colon])
				rawValue := strings.TrimSpace(line[colon+1:])
				value := strings.Trim(rawValue, "\"'")
				switch key {
				case "title":
					task.Title = value
				case "status":
					task.Status = value
				case "assigned_to":
					task.AssignedTo = value
				case "substatus":
					task.Substatus = value
				default:
					task.Metadata[key] = rawValue
				}
			}
			body := strings.TrimSpace(parts[1])
			task.Description = body
		}
	}

	if task.Title == "" {
		return Task{}, fmt.Errorf("no title in %s", path)
	}
	if task.Status == "" {
		task.Status = "todo"
	}

	return task, nil
}

// ReadTask reads one task file and records its path.
func ReadTask(path string) (Task, error) {
	task, err := readTask(path)
	if err != nil {
		return Task{}, err
	}
	task.Path = path
	return task, nil
}

// WriteTask persists a complete task snapshot to its file.
func WriteTask(path string, task Task) error {
	return writeTask(path, task)
}

// AssignTaskAndStatus changes assignment and status in one Kanban write and
// returns the previous snapshot for transactional rollback by the caller.
func AssignTaskAndStatus(path, sessionID, newStatus string) (Task, Task, error) {
	previous, err := ReadTask(path)
	if err != nil {
		return Task{}, Task{}, err
	}
	updated := previous
	updated.AssignedTo = sessionID
	updated.Status = newStatus
	if newStatus != "progress" {
		updated.Substatus = ""
	}
	if err := WriteTask(path, updated); err != nil {
		return Task{}, Task{}, err
	}
	return previous, updated, nil
}

// UpdateStatus changes the status of a task file and returns the updated task.
// If the new status is anything other than "progress", the substatus is
// cleared — substatus only makes sense while a task is actively being
// worked on.
//
// A task currently in "done" cannot be moved back to "progress" — once a
// task is closed it must be re-opened via another transition (todo /
// pending) instead of reactivating it. Returns ErrDoneToProgressForbidden
// in that case so callers (UI buttons, scripts, external callers) can
// surface a clear message instead of silently rewriting the file.
func UpdateStatus(path, newStatus string) (Task, error) {
	task, err := readTask(path)
	if err != nil {
		return Task{}, err
	}
	if task.Status == "done" && newStatus == "progress" {
		return Task{}, ErrDoneToProgressForbidden
	}
	task.Status = newStatus
	if newStatus != "progress" {
		task.Substatus = ""
	}
	if err := writeTask(path, task); err != nil {
		return Task{}, err
	}
	task.Path = path
	return task, nil
}

// AssignTask sets the assigned_to field of a task file and returns the updated task.
func AssignTask(path, sessionID string) (Task, error) {
	task, err := readTask(path)
	if err != nil {
		return Task{}, err
	}
	task.AssignedTo = sessionID
	if err := writeTask(path, task); err != nil {
		return Task{}, err
	}
	task.Path = path
	return task, nil
}

// UpdateSubstatus changes the substatus field of a task file. Substatus is
// only meaningful while Status == "progress" — the caller is expected to
// keep Status consistent. Returns the updated task.
func UpdateSubstatus(path, substatus string) (Task, error) {
	task, err := readTask(path)
	if err != nil {
		return Task{}, err
	}
	task.Substatus = substatus
	if err := writeTask(path, task); err != nil {
		return Task{}, err
	}
	task.Path = path
	return task, nil
}

// writeTask writes a task back to its .md file.
func writeTask(path string, task Task) error {
	var b strings.Builder
	b.WriteString("---\n")
	b.WriteString(fmt.Sprintf("title: %s\n", task.Title))
	b.WriteString(fmt.Sprintf("status: %s\n", task.Status))
	if task.AssignedTo != "" {
		b.WriteString(fmt.Sprintf("assigned_to: %s\n", task.AssignedTo))
	}
	if task.Substatus != "" {
		b.WriteString(fmt.Sprintf("substatus: %s\n", task.Substatus))
	}
	keys := make([]string, 0, len(task.Metadata))
	for key := range task.Metadata {
		if key == "" || key == "title" || key == "status" || key == "assigned_to" || key == "substatus" {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		b.WriteString(fmt.Sprintf("%s: %s\n", key, task.Metadata[key]))
	}
	b.WriteString("---\n")
	if task.Description != "" {
		b.WriteString(task.Description)
		b.WriteString("\n")
	}

	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".kanban-write-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o644); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.WriteString(b.String()); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}
