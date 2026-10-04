package human

import (
	"encoding/json"
	"strings"
	"testing"
)

const compositeProviderToolID = "call_fixture_01|fc_fixture_01"

func withCompositeProviderToolIDs(entry testEntry) testEntry {
	items := append([]testItem(nil), (*entry.Items)...)
	for index := range items {
		switch items[index].Kind {
		case "toolCall":
			items[index].ID = "tool-call-" + compositeProviderToolID
			items[index].ToolCallID = mustPointer(compositeProviderToolID)
		case "toolResult":
			items[index].ID = "tool-result-" + compositeProviderToolID
			items[index].ToolCallID = mustPointer(compositeProviderToolID)
		}
	}
	entry.Items = &items
	return entry
}

func TestReadFSPreservesCompositeProviderToolIDs(t *testing.T) {
	for _, source := range []string{"history", "live"} {
		t.Run(source, func(t *testing.T) {
			history, live, input := testFixture()
			history.Entries[1] = withCompositeProviderToolIDs(history.Entries[1])
			live.Entry = json.RawMessage("null")
			if source == "live" {
				activity := withCompositeProviderToolIDs(history.Entries[1])
				(*activity.Items)[2].Text = "live result"
				live.Entry = mustJSON(activity)
			}
			data, err := readFS(projectionFS(t, history, live, input), testProfile, testSessionID)
			if err != nil {
				t.Fatal(err)
			}
			if len(data.Entries) != 2 || len(data.Entries[1].Items) != 3 {
				t.Fatal("composite provider ID changed the activity grouping")
			}
			items := data.Entries[1].Items
			if items[1].ID != "tool-call-"+compositeProviderToolID || items[2].ID != "tool-result-"+compositeProviderToolID {
				t.Fatal("reader changed the original provider-linked item IDs")
			}
			if items[1].ToolCallID != compositeProviderToolID || items[2].ToolCallID != compositeProviderToolID {
				t.Fatal("reader changed the composite tool association")
			}
			if source == "live" && items[2].Text != "live result" {
				t.Fatal("live result did not replace the current activity")
			}
		})
	}
}

func TestReadFSPreservesCompositeProviderIDsAcrossAnswerBoundary(t *testing.T) {
	for _, source := range []string{"history", "live"} {
		t.Run(source, func(t *testing.T) {
			history, live, input := testFixture()
			activity := withCompositeProviderToolIDs(history.Entries[1])
			items := *activity.Items
			activity.Items = &[]testItem{items[1]}
			activity.Status = mustPointer("done")
			history.Entries[1] = activity
			history.Entries = append(history.Entries, testEntry{Kind: "answer", ID: "answer-1", Text: mustPointer("final")})
			following := testEntry{Kind: "activity", ID: "activity-2", Status: mustPointer("working"), Items: &[]testItem{items[2]}}
			live.Entry = json.RawMessage("null")
			if source == "live" {
				live.Entry = mustJSON(following)
			} else {
				history.Entries = append(history.Entries, following)
			}
			data, err := readFS(projectionFS(t, history, live, input), testProfile, testSessionID)
			if err != nil {
				t.Fatal(err)
			}
			if len(data.Entries) != 4 || len(data.Entries[1].Items) != 1 || len(data.Entries[3].Items) != 1 {
				t.Fatal("composite provider ID duplicated or reordered call/result")
			}
			if data.Entries[1].Items[0].ToolCallID != compositeProviderToolID || data.Entries[3].Items[0].ToolCallID != compositeProviderToolID {
				t.Fatal("result lost the preceding composite provider call ID")
			}
		})
	}
}

func TestReadFSRejectsUnsafeCompositeProviderIdentifiers(t *testing.T) {
	cases := []struct {
		name  string
		value string
	}{
		{name: "slash", value: "call_fixture|fc/fixture"},
		{name: "backslash", value: "call_fixture|fc\\fixture"},
		{name: "space", value: "call_fixture|fc fixture"},
		{name: "unicode space", value: "call_fixture|fc\u00a0fixture"},
		{name: "control", value: "call_fixture|fc\x1bfixture"},
		{name: "null", value: "call_fixture|fc\x00fixture"},
		{name: "line feed", value: "call_fixture|fc\nfixture"},
		{name: "other punctuation", value: "call_fixture|fc?fixture"},
		{name: "overflow", value: "call|" + strings.Repeat("f", maxIdentifier)},
		{name: "empty", value: ""},
		{name: "dot", value: "."},
		{name: "dotdot", value: ".."},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			for _, field := range []string{"item ID", "tool call association", "tool result association"} {
				t.Run(field, func(t *testing.T) {
					history, live, input := testFixture()
					live.Entry = json.RawMessage("null")
					items := *history.Entries[1].Items
					switch field {
					case "item ID":
						items[1].ID = testCase.value
					case "tool call association":
						items[1].ToolCallID = mustPointer(testCase.value)
					case "tool result association":
						items[2].ToolCallID = mustPointer(testCase.value)
					}
					if _, err := readFS(projectionFS(t, history, live, input), testProfile, testSessionID); err == nil {
						t.Fatal("unsafe provider identifier was accepted")
					}
				})
			}
		})
	}
}

func TestReadFSRejectsInvalidCompositeProviderCorrelation(t *testing.T) {
	for _, kind := range []string{"orphan", "duplicate result", "duplicate call"} {
		t.Run(kind, func(t *testing.T) {
			history, live, input := testFixture()
			activity := withCompositeProviderToolIDs(history.Entries[1])
			items := *activity.Items
			activity.Status = mustPointer("done")
			following := []testItem{items[2]}
			following[0].ID = "result-repeat"
			switch kind {
			case "orphan":
				activity.Items = &[]testItem{items[0]}
			case "duplicate call":
				following[0] = items[1]
				following[0].ID = "call-repeat"
			}
			history.Entries[1] = activity
			history.Entries = append(history.Entries, testEntry{Kind: "answer", ID: "answer-1", Text: mustPointer("final")})
			live.Entry = mustJSON(testEntry{Kind: "activity", ID: "activity-2", Status: mustPointer("working"), Items: &following})
			if _, err := readFS(projectionFS(t, history, live, input), testProfile, testSessionID); err == nil {
				t.Fatal("invalid composite provider correlation was accepted")
			}
		})
	}
}

func TestCompositeProjectionIdentifierLengthLimit(t *testing.T) {
	prefix := "call_fixture|fc_"
	boundary := prefix + strings.Repeat("a", maxIdentifier-len(prefix))
	if !validIdentifier(boundary) || validIdentifier(boundary+"a") {
		t.Fatal("composite identifiers changed the existing byte-length limit")
	}
}

func TestProjectionIdentifierSeparatorDoesNotRelaxFilesystemIdentity(t *testing.T) {
	for _, identity := range []struct {
		name      string
		profile   string
		sessionID string
	}{
		{name: "profile", profile: "pro|file", sessionID: testSessionID},
		{name: "session", profile: testProfile, sessionID: "profile__call|fc"},
	} {
		t.Run(identity.name, func(t *testing.T) {
			if Directory(identity.profile, identity.sessionID) != "" {
				t.Fatal("projection separator was accepted in a filesystem identity")
			}
			if _, err := validateIdentity(identity.profile, identity.sessionID); err == nil {
				t.Fatal("invalid filesystem identity was accepted")
			}
		})
	}
}
