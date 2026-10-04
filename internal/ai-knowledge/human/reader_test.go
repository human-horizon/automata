package human

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/HumanHorizon/automata/internal/paths"
)

const (
	testProfile   = "profile"
	testSessionID = "profile__chat"
	testEpoch     = "epoch-1"
)

type testHistory struct {
	SchemaVersion uint64      `json:"schemaVersion"`
	Profile       string      `json:"profile"`
	SessionID     string      `json:"sessionId"`
	Epoch         string      `json:"epoch"`
	Revision      uint64      `json:"revision"`
	Entries       []testEntry `json:"entries"`
}

type testLive struct {
	SchemaVersion   uint64          `json:"schemaVersion"`
	Profile         string          `json:"profile"`
	SessionID       string          `json:"sessionId"`
	Epoch           string          `json:"epoch"`
	Revision        uint64          `json:"revision"`
	HistoryRevision uint64          `json:"historyRevision"`
	Entry           json.RawMessage `json:"entry"`
}

type testInput struct {
	SchemaVersion uint64          `json:"schemaVersion"`
	Profile       string          `json:"profile"`
	SessionID     string          `json:"sessionId"`
	Epoch         string          `json:"epoch"`
	Revision      uint64          `json:"revision"`
	Width         int             `json:"width"`
	Lines         []string        `json:"lines"`
	Cursor        json.RawMessage `json:"cursor"`
	Native        bool            `json:"native"`
}

type testEntry struct {
	Kind      string      `json:"kind"`
	ID        string      `json:"id"`
	Text      *string     `json:"text,omitempty"`
	Truncated *bool       `json:"truncated,omitempty"`
	Status    *string     `json:"status,omitempty"`
	Items     *[]testItem `json:"items,omitempty"`
}

type testItem struct {
	Kind       string  `json:"kind"`
	ID         string  `json:"id"`
	Text       string  `json:"text"`
	ToolCallID *string `json:"toolCallId,omitempty"`
	Name       *string `json:"name,omitempty"`
	IsError    *bool   `json:"isError,omitempty"`
}

func testFixture() (testHistory, testLive, testInput) {
	callID := "call-1"
	toolName := "search"
	items := []testItem{
		{Kind: "thinking", ID: "thinking-1", Text: "reasoning"},
		{Kind: "toolCall", ID: "call-item-1", Text: "search(query)", ToolCallID: &callID, Name: &toolName},
		{Kind: "toolResult", ID: "result-1", Text: "found", ToolCallID: &callID, Name: &toolName},
	}
	status := "working"
	activity := testEntry{Kind: "activity", ID: "activity-1", Status: &status, Items: &items}
	question := "question"
	history := testHistory{
		SchemaVersion: 1,
		Profile:       testProfile,
		SessionID:     testSessionID,
		Epoch:         testEpoch,
		Revision:      4,
		Entries: []testEntry{
			{Kind: "user", ID: "user-1", Text: &question},
			activity,
		},
	}
	liveItems := []testItem{
		{Kind: "thinking", ID: "thinking-1", Text: "updated reasoning"},
		{Kind: "toolCall", ID: "call-item-1", Text: "search(query)", ToolCallID: &callID, Name: &toolName},
		{Kind: "toolResult", ID: "result-1", Text: "updated result", ToolCallID: &callID, Name: &toolName},
	}
	liveStatus := "working"
	liveActivity := testEntry{Kind: "activity", ID: "activity-1", Status: &liveStatus, Items: &liveItems}
	liveEntry := mustJSON(liveActivity)
	live := testLive{
		SchemaVersion:   1,
		Profile:         testProfile,
		SessionID:       testSessionID,
		Epoch:           testEpoch,
		Revision:        6,
		HistoryRevision: history.Revision,
		Entry:           liveEntry,
	}
	input := testInput{
		SchemaVersion: 1,
		Profile:       testProfile,
		SessionID:     testSessionID,
		Epoch:         testEpoch,
		Revision:      8,
		Width:         80,
		Lines:         []string{"draft", "second line"},
		Cursor:        json.RawMessage(`{"row":1,"col":4}`),
		Native:        true,
	}
	return history, live, input
}

func projectionFS(t *testing.T, history testHistory, live testLive, input testInput) fstest.MapFS {
	t.Helper()
	return fstest.MapFS{
		historyName: &fstest.MapFile{Data: mustJSON(history)},
		liveName:    &fstest.MapFile{Data: mustJSON(live)},
		inputName:   &fstest.MapFile{Data: mustJSON(input)},
	}
}

func mustJSON(value any) []byte {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return data
}

func mustPointer[T any](value T) *T {
	return &value
}

func TestReadFSMergesLiveActivityByStableIDAndPreservesContent(t *testing.T) {
	history, live, input := testFixture()
	longText := strings.Repeat("visible text ", 10_000) + "\x1b[31m"
	liveEntry := testEntry{
		Kind:   "activity",
		ID:     "activity-1",
		Status: mustPointer("working"),
		Items:  &[]testItem{{Kind: "commentary", ID: "commentary-1", Text: longText}},
	}
	live.Entry = mustJSON(liveEntry)

	data, err := readFS(projectionFS(t, history, live, input), testProfile, testSessionID)
	if err != nil {
		t.Fatalf("readFS returned error: %v", err)
	}
	if len(data.Entries) != 2 {
		t.Fatalf("merged entries = %d, want 2", len(data.Entries))
	}
	activity := data.Entries[1]
	if activity.ID != "activity-1" || activity.Status != ActivityStatusWorking || len(activity.Items) != 1 {
		t.Fatalf("live activity did not replace same-ID history entry: %#v", activity)
	}
	if activity.Items[0].Text != longText {
		t.Fatal("reader truncated or sanitized visible activity text")
	}
	if !strings.Contains(activity.Items[0].Text, "\x1b") {
		t.Fatal("reader altered model/tool text controls owned by the UI sanitizer")
	}
	if data.Input.Revision != 8 || data.Input.Width != 80 || len(data.Input.Lines) != 2 || data.Input.Cursor == nil || data.Input.Cursor.Row != 1 || !data.Input.Native {
		t.Fatalf("input frame was not preserved: %#v", data.Input)
	}
	if data.Epoch != testEpoch || data.HistoryRevision != 4 || data.LiveRevision != 6 {
		t.Fatalf("projection header = epoch %q history %d live %d", data.Epoch, data.HistoryRevision, data.LiveRevision)
	}
}

func TestReadFSAppendsLiveActivityOnlyWhenLastCompatible(t *testing.T) {
	history, live, input := testFixture()
	question := "question"
	history.Entries = []testEntry{{Kind: "user", ID: "user-1", Text: &question}}
	liveActivity := testEntry{
		Kind:   "activity",
		ID:     "activity-2",
		Status: mustPointer("working"),
		Items:  &[]testItem{{Kind: "thinking", ID: "thinking-2", Text: "working"}},
	}
	live.Entry = mustJSON(liveActivity)

	data, err := readFS(projectionFS(t, history, live, input), testProfile, testSessionID)
	if err != nil {
		t.Fatalf("readFS did not append compatible activity: %v", err)
	}
	if len(data.Entries) != 2 || data.Entries[1].ID != "activity-2" {
		t.Fatalf("appended entries = %#v", data.Entries)
	}

	answer := "answer"
	history.Entries = append(history.Entries, testEntry{Kind: "answer", ID: "answer-1", Text: &answer})
	live.HistoryRevision = history.Revision
	data, err = readFS(projectionFS(t, history, live, input), testProfile, testSessionID)
	if err != nil {
		t.Fatalf("readFS did not append a new chain after an answer: %v", err)
	}
	if len(data.Entries) != 3 || data.Entries[2].ID != "activity-2" {
		t.Fatalf("activity after answer was not appended: %#v", data.Entries)
	}

	history.Entries = []testEntry{}
	data, err = readFS(projectionFS(t, history, live, input), testProfile, testSessionID)
	if err != nil || len(data.Entries) != 1 || data.Entries[0].ID != "activity-2" {
		t.Fatalf("live activity without a user precondition = %#v, %v", data, err)
	}

	question = "question"
	status := "working"
	items := []testItem{}
	history.Entries = []testEntry{
		{Kind: "user", ID: "user-1", Text: &question},
		{Kind: "activity", ID: "activity-1", Status: &status, Items: &items},
	}
	if _, err := readFS(projectionFS(t, history, live, input), testProfile, testSessionID); err == nil {
		t.Fatal("new live activity duplicated an open chain")
	}
}

func TestReadFSAllowsUserRowsAfterAnOpenActivity(t *testing.T) {
	history, live, input := testFixture()
	steering := "follow-up"
	history.Entries = append(history.Entries, testEntry{Kind: "user", ID: "user-2", Text: &steering})

	data, err := readFS(projectionFS(t, history, live, input), testProfile, testSessionID)
	if err != nil {
		t.Fatalf("live update after steering row was rejected: %v", err)
	}
	if len(data.Entries) != 3 || data.Entries[1].ID != "activity-1" || data.Entries[2].ID != "user-2" {
		t.Fatalf("open chain order changed after steering row: %#v", data.Entries)
	}
}

func TestReadFSRejectsLiveReplacementOfClosedActivity(t *testing.T) {
	history, live, input := testFixture()
	answer := "final answer"
	history.Entries = append(history.Entries, testEntry{Kind: "answer", ID: "answer-1", Text: &answer})
	history.Revision++
	live.HistoryRevision = history.Revision

	if _, err := readFS(projectionFS(t, history, live, input), testProfile, testSessionID); err == nil {
		t.Fatal("live activity resurrected a chain with a later final answer")
	}
}

func TestReadFSPreservesNullLiveEntryAndEmptyInput(t *testing.T) {
	history, live, input := testFixture()
	live.Entry = json.RawMessage("null")
	input.Lines = []string{}
	input.Cursor = json.RawMessage("null")

	data, err := readFS(projectionFS(t, history, live, input), testProfile, testSessionID)
	if err != nil {
		t.Fatalf("readFS returned error: %v", err)
	}
	if len(data.Entries) != len(history.Entries) || data.Input.Cursor != nil || data.Input.Lines == nil {
		t.Fatalf("null/empty fields were not preserved: %#v", data)
	}
}

func TestReadFSRejectsHeaderAndRevisionMismatches(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*testHistory, *testLive, *testInput)
	}{
		{name: "unknown schema", mutate: func(history *testHistory, _ *testLive, _ *testInput) { history.SchemaVersion = 2 }},
		{name: "history profile", mutate: func(history *testHistory, _ *testLive, _ *testInput) { history.Profile = "other" }},
		{name: "history session", mutate: func(history *testHistory, _ *testLive, _ *testInput) { history.SessionID = "other__chat" }},
		{name: "history zero revision", mutate: func(history *testHistory, _ *testLive, _ *testInput) { history.Revision = 0 }},
		{name: "live epoch", mutate: func(_ *testHistory, live *testLive, _ *testInput) { live.Epoch = "epoch-2" }},
		{name: "input epoch", mutate: func(_ *testHistory, _ *testLive, input *testInput) { input.Epoch = "epoch-2" }},
		{name: "live history revision", mutate: func(_ *testHistory, live *testLive, _ *testInput) { live.HistoryRevision++ }},
		{name: "live profile", mutate: func(_ *testHistory, live *testLive, _ *testInput) { live.Profile = "other" }},
		{name: "input session", mutate: func(_ *testHistory, _ *testLive, input *testInput) { input.SessionID = "other__chat" }},
		{name: "input zero revision", mutate: func(_ *testHistory, _ *testLive, input *testInput) { input.Revision = 0 }},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			history, live, input := testFixture()
			testCase.mutate(&history, &live, &input)
			if _, err := readFS(projectionFS(t, history, live, input), testProfile, testSessionID); err == nil {
				t.Fatal("mismatched header or revision was accepted")
			}
		})
	}
}

func TestReadFSRejectsInvalidEntryAndItemContracts(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*testHistory, *testLive)
	}{
		{name: "unknown entry kind", mutate: func(history *testHistory, _ *testLive) { history.Entries[0].Kind = "message" }},
		{name: "control in entry ID", mutate: func(history *testHistory, _ *testLive) { history.Entries[0].ID = "user\x1b" }},
		{name: "unknown activity status", mutate: func(history *testHistory, _ *testLive) { *history.Entries[1].Status = "pending" }},
		{name: "duplicate entry ID", mutate: func(history *testHistory, _ *testLive) { history.Entries[1].ID = history.Entries[0].ID }},
		{name: "missing tool call association", mutate: func(history *testHistory, _ *testLive) {
			(*history.Entries[1].Items)[2].ToolCallID = mustPointer("missing")
		}},
		{name: "tool result before call", mutate: func(history *testHistory, _ *testLive) {
			items := *history.Entries[1].Items
			items[1], items[2] = items[2], items[1]
		}},
		{name: "duplicate tool result", mutate: func(history *testHistory, _ *testLive) {
			items := *history.Entries[1].Items
			items = append(items, items[2])
			history.Entries[1].Items = &items
		}},
		{name: "live non-activity", mutate: func(_ *testHistory, live *testLive) {
			live.Entry = mustJSON(testEntry{Kind: "user", ID: "not-activity", Text: mustPointer("x")})
		}},
		{name: "duplicate activity groups", mutate: func(history *testHistory, _ *testLive) {
			status := "working"
			items := []testItem{}
			history.Entries = append(history.Entries, testEntry{Kind: "activity", ID: "activity-2", Status: &status, Items: &items})
		}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			history, live, input := testFixture()
			live.Entry = json.RawMessage("null")
			testCase.mutate(&history, &live)
			if _, err := readFS(projectionFS(t, history, live, input), testProfile, testSessionID); err == nil {
				t.Fatal("invalid entry/item contract was accepted")
			}
		})
	}
}

func TestReadFSRejectsMalformedStrictJSONAndInvalidNumbers(t *testing.T) {
	history, live, input := testFixture()
	base := projectionFS(t, history, live, input)
	cases := []struct {
		name string
		file string
		data []byte
	}{
		{name: "unknown field", file: historyName, data: []byte(strings.TrimSuffix(string(base[historyName].Data), "}") + `,"unknown":true}`)},
		{name: "duplicate field", file: historyName, data: []byte(strings.Replace(string(base[historyName].Data), `"profile":"profile"`, `"profile":"profile","profile":"profile"`, 1))},
		{name: "multiple values", file: historyName, data: append(bytesOf(base[historyName].Data), []byte(` {}`)...)},
		{name: "invalid UTF-8", file: historyName, data: append(bytesOf(base[historyName].Data), 0xff)},
		{name: "revision overflow", file: historyName, data: []byte(strings.Replace(string(base[historyName].Data), `"revision":4`, `"revision":18446744073709551616`, 1))},
		{name: "width overflow", file: inputName, data: []byte(strings.Replace(string(base[inputName].Data), `"width":80`, `"width":9223372036854775808`, 1))},
		{name: "live revision overflow", file: liveName, data: []byte(strings.Replace(string(base[liveName].Data), `"revision":6`, `"revision":18446744073709551616`, 1))},
		{name: "input revision overflow", file: inputName, data: []byte(strings.Replace(string(base[inputName].Data), `"revision":8`, `"revision":18446744073709551616`, 1))},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			files := copyMapFS(base)
			files[testCase.file] = &fstest.MapFile{Data: testCase.data}
			if _, err := readFS(files, testProfile, testSessionID); err == nil {
				t.Fatal("malformed JSON was accepted")
			}
		})
	}
}

func TestReadFSValidatesInputFrameBounds(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*testInput)
	}{
		{name: "zero width", mutate: func(input *testInput) { input.Width = 0 }},
		{name: "negative width", mutate: func(input *testInput) { input.Width = -1 }},
		{name: "cursor row outside lines", mutate: func(input *testInput) { input.Cursor = json.RawMessage(`{"row":2,"col":0}`) }},
		{name: "cursor column outside width", mutate: func(input *testInput) { input.Cursor = json.RawMessage(`{"row":0,"col":80}`) }},
		{name: "negative cursor", mutate: func(input *testInput) { input.Cursor = json.RawMessage(`{"row":0,"col":-1}`) }},
		{name: "cursor missing col", mutate: func(input *testInput) { input.Cursor = json.RawMessage(`{"row":0}`) }},
		{name: "null lines", mutate: func(input *testInput) { input.Lines = nil }},
		{name: "non-native empty frame", mutate: func(input *testInput) { input.Native = false; input.Lines = []string{} }},
		{name: "embedded line feed", mutate: func(input *testInput) { input.Lines[0] = "first\nsecond" }},
		{name: "embedded carriage return", mutate: func(input *testInput) { input.Lines[0] = "first\rsecond" }},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			history, live, input := testFixture()
			testCase.mutate(&input)
			if _, err := readFS(projectionFS(t, history, live, input), testProfile, testSessionID); err == nil {
				t.Fatal("invalid input frame was accepted")
			}
		})
	}
}

func TestReadFSRejectsMissingCorruptAndNonRegularCandidates(t *testing.T) {
	history, live, input := testFixture()
	base := projectionFS(t, history, live, input)

	missing := copyMapFS(base)
	delete(missing, inputName)
	if _, err := readFS(missing, testProfile, testSessionID); err == nil {
		t.Fatal("missing required input file was accepted")
	}

	corrupt := copyMapFS(base)
	corrupt[liveName] = &fstest.MapFile{Data: []byte("{")}
	if _, err := readFS(corrupt, testProfile, testSessionID); err == nil {
		t.Fatal("corrupt live file was accepted")
	}

	symlink := copyMapFS(base)
	symlink[historyName] = &fstest.MapFile{Mode: fs.ModeSymlink, Data: []byte("outside.json")}
	if _, err := readFS(symlink, testProfile, testSessionID); err == nil {
		t.Fatal("symlink candidate was accepted")
	}
}

func TestDirectoryUsesCanonicalSessionPathAndRejectsUnsafeIdentity(t *testing.T) {
	t.Setenv("AI_DATA_HOME", t.TempDir())
	want := filepath.Join(paths.SessionDir(testProfile, testSessionID), directoryName)
	if got := Directory(testProfile, testSessionID); got != want {
		t.Fatalf("Directory() = %q, want %q", got, want)
	}
	for _, identity := range [][2]string{
		{"Profile Name", testSessionID},
		{"../profile", testSessionID},
		{testProfile, "../other"},
		{testProfile, "bad\x00session"},
	} {
		if got := Directory(identity[0], identity[1]); got != "" {
			t.Errorf("Directory(%q, %q) = %q, want invalid path", identity[0], identity[1], got)
		}
	}
	if got := Directory("", testSessionID); got != filepath.Join(paths.SessionDir("", testSessionID), directoryName) {
		t.Fatalf("default Directory() = %q", got)
	}
	unicodeProfile := paths.ProfileSlug("Проект Ω")
	if unicodeProfile != "proekt-ω" {
		t.Fatalf("ProfileSlug() = %q, want canonical Unicode slug", unicodeProfile)
	}
	unicodeSession := unicodeProfile + "__chat"
	if got := Directory(unicodeProfile, unicodeSession); got != filepath.Join(paths.SessionDir(unicodeProfile, unicodeSession), directoryName) {
		t.Fatalf("Unicode Directory() = %q", got)
	}
	if got := Directory("Проект Ω", unicodeSession); got != "" {
		t.Fatalf("Directory accepted a non-canonical profile name: %q", got)
	}
}

func TestReadForProfileRejectsSymlinkedAncestorsAndFiles(t *testing.T) {
	base := t.TempDir()
	t.Setenv("AI_DATA_HOME", base)
	outside := t.TempDir()
	outsideHuman := filepath.Join(outside, "human")
	if err := os.Mkdir(outsideHuman, 0o700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(outsideHuman, "do-not-delete")
	if err := os.WriteFile(marker, []byte("safe"), 0o600); err != nil {
		t.Fatal(err)
	}
	profileRoot := filepath.Join(base, "profiles", testProfile)
	if err := os.MkdirAll(filepath.Dir(profileRoot), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, profileRoot); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}
	if _, err := ReadForProfile(testProfile, testSessionID); err == nil {
		t.Fatal("reader followed a symlinked profile ancestor")
	}
	if err := ClearForProfile(testProfile, testSessionID); err == nil {
		t.Fatal("clear followed a symlinked profile ancestor")
	}
	if data, err := os.ReadFile(marker); err != nil || string(data) != "safe" {
		t.Fatalf("outside marker changed: data=%q err=%v", data, err)
	}

	t.Setenv("AI_DATA_HOME", t.TempDir())
	directory := Directory(testProfile, testSessionID)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	externalFile := filepath.Join(outside, "history.json")
	if err := os.WriteFile(externalFile, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(externalFile, filepath.Join(directory, historyName)); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}
	if _, err := ReadForProfile(testProfile, testSessionID); err == nil {
		t.Fatal("reader followed a symlinked projection file")
	}
}

func TestReadForProfileRejectsSymlinkedSessionDirectory(t *testing.T) {
	base := t.TempDir()
	t.Setenv("AI_DATA_HOME", base)
	outside := t.TempDir()
	if err := os.Mkdir(filepath.Join(outside, directoryName), 0o700); err != nil {
		t.Fatal(err)
	}
	sessions := filepath.Dir(paths.SessionDir(testProfile, testSessionID))
	if err := os.MkdirAll(sessions, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, paths.SessionDir(testProfile, testSessionID)); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}
	if _, err := ReadForProfile(testProfile, testSessionID); err == nil {
		t.Fatal("reader followed a symlinked session directory")
	}
	if err := ClearForProfile(testProfile, testSessionID); err == nil {
		t.Fatal("clear followed a symlinked session directory")
	}
	if _, err := os.Stat(filepath.Join(outside, directoryName)); err != nil {
		t.Fatalf("outside Human directory was touched: %v", err)
	}
}

func TestClearForProfileRemovesOnlyValidatedHumanDirectory(t *testing.T) {
	base := t.TempDir()
	t.Setenv("AI_DATA_HOME", base)
	profile := testProfile
	sessionID := testSessionID
	humanDirectory := Directory(profile, sessionID)
	if err := os.MkdirAll(humanDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(humanDirectory, "marker"), []byte("projection"), 0o600); err != nil {
		t.Fatal(err)
	}
	sessionDirectory := paths.SessionDir(profile, sessionID)
	sibling := filepath.Join(sessionDirectory, "plans.json")
	if err := os.WriteFile(sibling, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	otherHuman := Directory(profile, "profile__other")
	if err := os.MkdirAll(otherHuman, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(otherHuman, "marker"), []byte("other"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := ClearForProfile(profile, sessionID); err != nil {
		t.Fatalf("ClearForProfile returned error: %v", err)
	}
	if _, err := os.Lstat(humanDirectory); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("current human directory remains: %v", err)
	}
	if contents, err := os.ReadFile(sibling); err != nil || string(contents) != "keep" {
		t.Fatalf("sibling history data changed: contents=%q err=%v", contents, err)
	}
	if contents, err := os.ReadFile(filepath.Join(otherHuman, "marker")); err != nil || string(contents) != "other" {
		t.Fatalf("other session projection changed: contents=%q err=%v", contents, err)
	}
	if err := ClearForProfile(profile, sessionID); err != nil {
		t.Fatalf("repeated missing clear returned error: %v", err)
	}
	if err := ClearForProfile(profile, "profile__missing"); err != nil {
		t.Fatalf("missing session clear returned error: %v", err)
	}
}

func TestClearForProfileRejectsSymlinkOrNonDirectoryTarget(t *testing.T) {
	base := t.TempDir()
	t.Setenv("AI_DATA_HOME", base)
	outside := t.TempDir()
	marker := filepath.Join(outside, "marker")
	if err := os.WriteFile(marker, []byte("safe"), 0o600); err != nil {
		t.Fatal(err)
	}
	profileRoot := paths.SessionDir(testProfile, testSessionID)
	if err := os.MkdirAll(profileRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, Directory(testProfile, testSessionID)); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}
	if err := ClearForProfile(testProfile, testSessionID); err == nil {
		t.Fatal("clear accepted a symlink projection directory")
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("outside marker was removed: %v", err)
	}

	if err := os.Remove(Directory(testProfile, testSessionID)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(Directory(testProfile, testSessionID), []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ClearForProfile(testProfile, testSessionID); err == nil {
		t.Fatal("clear accepted a non-directory projection path")
	}
}

func copyMapFS(source fstest.MapFS) fstest.MapFS {
	copy := make(fstest.MapFS, len(source))
	for name, entry := range source {
		copy[name] = &fstest.MapFile{Data: append([]byte(nil), entry.Data...), Mode: entry.Mode, ModTime: entry.ModTime, Sys: entry.Sys}
	}
	return copy
}

func bytesOf(data []byte) []byte {
	return append([]byte(nil), data...)
}
