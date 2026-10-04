package human

import (
	"encoding/json"
	"testing"
)

func TestReadFSToolResultsCanFollowAnExplicitFinalBoundary(t *testing.T) {
	for _, liveResult := range []bool{false, true} {
		name := "history"
		if liveResult {
			name = "live"
		}
		t.Run(name, func(t *testing.T) {
			history, live, input := testFixture()
			items := *history.Entries[1].Items
			call := []testItem{items[1]}
			result := []testItem{items[2]}
			done, working := "done", "working"
			history.Entries[1].Items, history.Entries[1].Status = &call, &done
			answer := "Explicit final before the pending result"
			history.Entries = append(history.Entries, testEntry{Kind: "answer", ID: "answer-1", Text: &answer})
			following := testEntry{Kind: "activity", ID: "activity-2", Status: &working, Items: &result}
			if liveResult {
				live.Entry = mustJSON(following)
			} else {
				history.Entries = append(history.Entries, following)
				live.Entry = json.RawMessage("null")
			}
			data, err := readFS(projectionFS(t, history, live, input), testProfile, testSessionID)
			if err != nil {
				t.Fatal(err)
			}
			if len(data.Entries) != 4 || len(data.Entries[1].Items) != 1 || len(data.Entries[3].Items) != 1 {
				t.Fatalf("call/result were duplicated or chronology changed: %#v", data.Entries)
			}
			if data.Entries[3].Items[0].Kind != ItemKindToolResult || data.Entries[3].Items[0].ToolCallID != data.Entries[1].Items[0].ToolCallID {
				t.Fatal("result lost its preceding call reference")
			}
		})
	}
}

func TestReadFSRejectsInvalidGlobalToolCorrelation(t *testing.T) {
	for _, kind := range []string{"orphan", "duplicate result", "duplicate call"} {
		t.Run(kind, func(t *testing.T) {
			history, live, input := testFixture()
			items := *history.Entries[1].Items
			done, working := "done", "working"
			history.Entries[1].Status = &done
			answer := "final"
			history.Entries = append(history.Entries, testEntry{Kind: "answer", ID: "answer-1", Text: &answer})
			following := []testItem{items[2]}
			following[0].ID = "result-2"
			switch kind {
			case "orphan":
				earlier := []testItem{items[0]}
				history.Entries[1].Items = &earlier
			case "duplicate call":
				following[0] = items[1]
				following[0].ID = "call-item-2"
			}
			live.Entry = mustJSON(testEntry{Kind: "activity", ID: "activity-2", Status: &working, Items: &following})
			if _, err := readFS(projectionFS(t, history, live, input), testProfile, testSessionID); err == nil {
				t.Fatal("invalid cross-answer tool correlation was accepted")
			}
		})
	}
}
