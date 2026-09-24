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
	"gopkg.in/yaml.v3"
)

// ErrDoneToProgressForbidden is returned by UpdateStatus when a caller tries
// to move a task back from "done" to "progress". Once a task is done it is
// considered closed and the UI no longer renders a transition for it; this
// error guards against race conditions, scripted edits, or direct .md file
// rewrites that try to bypass the UI gate.
var ErrDoneToProgressForbidden = errors.New("kanban: task in done cannot be moved back to progress")

// ErrInvalidStatus is returned when a task contains or receives an unsupported status.
var ErrInvalidStatus = errors.New("kanban: invalid task status")

var validStatuses = map[string]struct{}{
	"todo":     {},
	"pending":  {},
	"progress": {},
	"done":     {},
}

// Task represents a single kanban task.
type Task struct {
	Title        string            `yaml:"title"`
	Status       string            `yaml:"status"`
	AssignedTo   string            `yaml:"assigned_to"`
	Substatus    string            `yaml:"substatus"`
	Description  string            // body of the .md file (after frontmatter)
	Path         string            // full path to the .md file
	Metadata     map[string]string // unknown scalar frontmatter fields
	frontmatter  *yaml.Node
	metadataKeys map[string]struct{}
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

	frontmatterText, body, err := splitFrontmatter(string(data))
	if err != nil {
		return Task{}, fmt.Errorf("read kanban frontmatter %s: %w", path, err)
	}
	var document yaml.Node
	if err := yaml.Unmarshal([]byte(frontmatterText), &document); err != nil {
		return Task{}, fmt.Errorf("parse kanban frontmatter %s: %w", path, err)
	}
	if len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode {
		return Task{}, fmt.Errorf("kanban frontmatter in %s must be a YAML mapping", path)
	}

	task := Task{
		Metadata:     make(map[string]string),
		metadataKeys: make(map[string]struct{}),
		frontmatter:  document.Content[0],
		Description:  body,
	}
	seen := make(map[string]struct{})
	statusFound := false
	for index := 0; index+1 < len(task.frontmatter.Content); index += 2 {
		keyNode := task.frontmatter.Content[index]
		valueNode := task.frontmatter.Content[index+1]
		if keyNode.Kind != yaml.ScalarNode || keyNode.Tag != "!!str" {
			continue
		}
		key := keyNode.Value
		if _, exists := seen[key]; exists && isKnownField(key) {
			return Task{}, fmt.Errorf("kanban frontmatter in %s has duplicate %q field", path, key)
		}
		seen[key] = struct{}{}
		value, isString := scalarString(valueNode)
		switch key {
		case "title":
			if !isString {
				return Task{}, fmt.Errorf("kanban title in %s must be a string", path)
			}
			task.Title = value
		case "status":
			statusFound = true
			if !isString {
				return Task{}, invalidStatus(valueNode.Value)
			}
			task.Status = value
		case "assigned_to":
			if isString {
				task.AssignedTo = value
			}
		case "substatus":
			if isString {
				task.Substatus = value
			}
		default:
			if valueNode.Kind == yaml.ScalarNode {
				task.Metadata[key] = valueNode.Value
				task.metadataKeys[key] = struct{}{}
			}
		}
	}

	if task.Title == "" {
		return Task{}, fmt.Errorf("no title in %s", path)
	}
	if !statusFound {
		task.Status = "todo"
	}
	if err := validateStatus(task.Status); err != nil {
		return Task{}, err
	}
	return task, nil
}

func splitFrontmatter(content string) (string, string, error) {
	firstLineEnd := strings.IndexByte(content, '\n')
	if firstLineEnd < 0 || strings.TrimSuffix(content[:firstLineEnd], "\r") != "---" {
		return "", "", errors.New("missing opening YAML delimiter")
	}
	frontmatterStart := firstLineEnd + 1
	lineStart := frontmatterStart
	for lineStart < len(content) {
		lineEnd := strings.IndexByte(content[lineStart:], '\n')
		if lineEnd < 0 {
			lineEnd = len(content)
		} else {
			lineEnd += lineStart
		}
		line := strings.TrimSuffix(content[lineStart:lineEnd], "\r")
		if line == "---" {
			bodyStart := lineEnd
			if bodyStart < len(content) && content[bodyStart] == '\n' {
				bodyStart++
			}
			return content[frontmatterStart:lineStart], strings.TrimSpace(content[bodyStart:]), nil
		}
		if lineEnd == len(content) {
			break
		}
		lineStart = lineEnd + 1
	}
	return "", "", errors.New("missing closing YAML delimiter")
}

func scalarString(node *yaml.Node) (string, bool) {
	if node.Kind != yaml.ScalarNode || node.Tag != "!!str" {
		return "", false
	}
	return node.Value, true
}

func isKnownField(key string) bool {
	switch key {
	case "title", "status", "assigned_to", "substatus":
		return true
	default:
		return false
	}
}

func validateStatus(status string) error {
	if _, ok := validStatuses[status]; !ok {
		return invalidStatus(status)
	}
	return nil
}

func invalidStatus(status string) error {
	return fmt.Errorf("%w: %q", ErrInvalidStatus, status)
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
	if err := validateStatus(newStatus); err != nil {
		return Task{}, Task{}, err
	}
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
	if err := validateStatus(newStatus); err != nil {
		return Task{}, err
	}
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
	if task.Title == "" {
		return errors.New("kanban: task title cannot be empty")
	}
	if err := validateStatus(task.Status); err != nil {
		return err
	}
	mapping := cloneYAMLNode(task.frontmatter)
	if mapping == nil {
		mapping = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	}
	setMappingString(mapping, "title", task.Title, false)
	setMappingString(mapping, "status", task.Status, false)
	setMappingString(mapping, "assigned_to", task.AssignedTo, true)
	setMappingString(mapping, "substatus", task.Substatus, true)

	for key := range task.metadataKeys {
		if _, exists := task.Metadata[key]; !exists {
			removeMappingPair(mapping, key)
		}
	}
	metadataKeys := make([]string, 0, len(task.Metadata))
	for key := range task.Metadata {
		if key != "" && !isKnownField(key) {
			metadataKeys = append(metadataKeys, key)
		}
	}
	sort.Strings(metadataKeys)
	for _, key := range metadataKeys {
		index := mappingValueIndex(mapping, key)
		if index < 0 {
			appendMappingString(mapping, key, task.Metadata[key])
			continue
		}
		value := mapping.Content[index+1]
		if value.Kind == yaml.ScalarNode && value.Value != task.Metadata[key] {
			setMappingString(mapping, key, task.Metadata[key], false)
		}
	}

	frontmatter, err := yaml.Marshal(mapping)
	if err != nil {
		return fmt.Errorf("marshal kanban frontmatter: %w", err)
	}
	var content strings.Builder
	content.WriteString("---\n")
	content.Write(frontmatter)
	if len(frontmatter) == 0 || frontmatter[len(frontmatter)-1] != '\n' {
		content.WriteByte('\n')
	}
	content.WriteString("---\n")
	if task.Description != "" {
		content.WriteString(task.Description)
		content.WriteByte('\n')
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
	if _, err := tmp.WriteString(content.String()); err != nil {
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

func cloneYAMLNode(node *yaml.Node) *yaml.Node {
	seen := make(map[*yaml.Node]*yaml.Node)
	var clone func(*yaml.Node) *yaml.Node
	clone = func(current *yaml.Node) *yaml.Node {
		if current == nil {
			return nil
		}
		if existing, ok := seen[current]; ok {
			return existing
		}
		copy := *current
		seen[current] = &copy
		copy.Content = make([]*yaml.Node, len(current.Content))
		for index, child := range current.Content {
			copy.Content[index] = clone(child)
		}
		copy.Alias = clone(current.Alias)
		return &copy
	}
	return clone(node)
}

func mappingValueIndex(mapping *yaml.Node, key string) int {
	for index := 0; index+1 < len(mapping.Content); index += 2 {
		keyNode := mapping.Content[index]
		if keyNode.Kind == yaml.ScalarNode && keyNode.Tag == "!!str" && keyNode.Value == key {
			return index
		}
	}
	return -1
}

func setMappingString(mapping *yaml.Node, key, value string, omitEmpty bool) {
	index := mappingValueIndex(mapping, key)
	if omitEmpty && value == "" {
		if index >= 0 {
			mapping.Content = append(mapping.Content[:index], mapping.Content[index+2:]...)
		}
		return
	}
	if index < 0 {
		appendMappingString(mapping, key, value)
		return
	}
	current := mapping.Content[index+1]
	updated := *current
	updated.Kind = yaml.ScalarNode
	updated.Tag = "!!str"
	updated.Value = value
	updated.Style = 0
	updated.Content = nil
	updated.Alias = nil
	mapping.Content[index+1] = &updated
}

func appendMappingString(mapping *yaml.Node, key, value string) {
	mapping.Content = append(mapping.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value},
	)
}

func removeMappingPair(mapping *yaml.Node, key string) {
	index := mappingValueIndex(mapping, key)
	if index >= 0 {
		mapping.Content = append(mapping.Content[:index], mapping.Content[index+2:]...)
	}
}
