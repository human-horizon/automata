package e2e

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/HumanHorizon/automata/internal/ai-knowledge/human"
	"github.com/HumanHorizon/automata/internal/paths"
	"github.com/HumanHorizon/automata/internal/slug"
	"github.com/charmbracelet/x/ansi"
	"github.com/starframe-dev/cue-tty/pkg/cue"
)

const humanScreenWait = 6 * time.Second

var humanIntermediateMarkers = []string{
	"ThinkingSecretMarker",
	"CommentarySecretMarker",
	"ToolCallOneSecretMarker",
	"ToolResultOneSecretMarker",
	"ThinkingSecondSecretMarker",
	"ToolCallTwoSecretMarker",
	"ToolResultTwoSecretMarker",
}

var humanLiveChainTwoMarkers = []string{
	"ChainTwoThinkingLiveMarker",
	"ChainTwoCommentaryLiveMarker",
	"ChainTwoToolCallLiveMarker",
	"ChainTwoToolResultLiveMarker",
}

func TestHumanChatFakePiPTY(t *testing.T) {
	const (
		profile  = "human-e2e-main"
		chatName = "human-pty-chat"
	)
	page, sessionID, humanDir := launchHumanPTY(t, profile, chatName)
	t.Cleanup(func() {
		if t.Failed() {
			if err := saveTestArtifact(page, "HumanFakePiPTY"); err != nil {
				t.Logf("save HumanFakePiPTY artifact: %v", err)
			}
		}
	})
	verifyHumanFixtureProjection(t, profile, sessionID, humanDir)
	assertProjectionIsolation(t, profile, sessionID)

	buttonX, buttonY, enabled := findHumanButton(t, page)
	if enabled {
		t.Fatal("Human started enabled; each tab must initially show native Pi")
	}
	clickHuman(t, page, buttonX, buttonY)
	focusHumanPTY(t, page)

	collapsed := waitForHumanScreen(t, page, "collapsed Human projection", func(screen string) bool {
		return strings.Contains(screen, "UserVisibleMarker") && strings.Contains(screen, "AnswerVisibleMarker") && humanButtonEnabled(screen)
	})
	assertOneHumanActivityRow(t, collapsed)
	assertMarkersAbsent(t, collapsed, humanIntermediateMarkers...)

	activityX, activityY := findHumanActivityRow(t, page)
	requireCue(t, "open activity chain", page.MouseClick(activityX, activityY))
	detail := waitForHumanScreen(t, page, "expanded activity chain", func(screen string) bool {
		if !strings.Contains(screen, "UserVisibleMarker") || !strings.Contains(screen, "AnswerVisibleMarker") {
			return false
		}
		for _, marker := range humanIntermediateMarkers {
			if !strings.Contains(screen, marker) {
				return false
			}
		}
		return true
	})
	assertOneHumanActivityRow(t, detail)
	assertMarkersChronological(t, detail, humanIntermediateMarkers)
	if strings.Index(detail, "ToolResultTwoSecretMarker") > strings.Index(detail, "UserVisibleMarker") {
		t.Fatal("activity chain detail was not rendered above the conversation")
	}

	activityX, activityY = findHumanActivityRow(t, page)
	requireCue(t, "close activity chain by clicking its row again", page.MouseClick(activityX, activityY))
	collapsed = waitForHumanScreen(t, page, "collapsed activity chain", func(screen string) bool {
		return strings.Contains(screen, "UserVisibleMarker") && !strings.Contains(screen, "ThinkingSecretMarker")
	})
	assertOneHumanActivityRow(t, collapsed)
	assertMarkersAbsent(t, collapsed, humanIntermediateMarkers...)
	focusHumanPTY(t, page)

	const draft = "DraftVisibleMarker"
	requireCue(t, "type native editor draft", page.Type(draft))
	waitHumanInput(t, profile, sessionID, func(input human.Input) bool {
		return strings.Contains(strings.Join(input.Lines, "\n"), draft)
	}, "typed draft")
	if err := page.WaitFor(draft, humanScreenWait); err != nil {
		t.Fatalf("typed draft was not rendered in Human: %v", err)
	}
	requireCue(t, "submit typed draft through Pi PTY", page.Press("Enter"))
	waitHumanFixtureInput(t, humanDir, draft)

	const pastePayload = "PasteLineOne\nPasteLineTwo"
	requireCue(t, "send bracketed paste through the terminal PTY", page.Type("\x1b[200~"+pastePayload+"\x1b[201~"))
	waitHumanInput(t, profile, sessionID, func(input human.Input) bool {
		return strings.Contains(strings.Join(input.Lines, "\n"), "PasteLineTwo")
	}, "multiline pasted draft")
	requireCue(t, "submit pasted draft through Pi PTY", page.Press("Enter"))
	waitHumanFixtureInput(t, humanDir, pastePayload)

	requireCue(t, "type live-update fixture command", page.Type("/live"))
	requireCue(t, "submit live-update fixture command", page.Press("Enter"))
	liveData := waitForHumanData(t, profile, sessionID, func(data *human.Data) bool {
		return len(data.Entries) == 5 &&
			data.Entries[0].ID == "user-1" &&
			data.Entries[1].ID == "activity-chain-1" &&
			data.Entries[2].ID == "answer-1" &&
			data.Entries[3].ID == "user-2" &&
			data.Entries[4].ID == "activity-chain-2" &&
			data.Entries[4].Status == human.ActivityStatusWorking
	}, "live chain-2 after the first final answer")
	assertLiveMarkersBelongToSecondChain(t, liveData, humanLiveChainTwoMarkers)
	liveCollapsed := waitForHumanScreen(t, page, "two collapsed answer-delimited activity chains", func(screen string) bool {
		return strings.Contains(screen, "в процессе") && strings.Contains(screen, "SecondUserVisibleMarker")
	})
	assertHumanActivityRowCount(t, liveCollapsed, 2)
	assertMarkersAbsent(t, liveCollapsed, humanIntermediateMarkers...)
	assertMarkersAbsent(t, liveCollapsed, humanLiveChainTwoMarkers...)
	activityX, activityY = findHumanActivityRowAt(t, page, 1)
	requireCue(t, "open the latest working activity chain", page.MouseClick(activityX, activityY))
	liveDetail := waitForHumanScreen(t, page, "chain-2 live details", func(screen string) bool {
		for _, marker := range humanLiveChainTwoMarkers {
			if !strings.Contains(screen, marker) {
				return false
			}
		}
		return strings.Contains(screen, "SecondUserVisibleMarker")
	})
	assertHumanActivityRowCount(t, liveDetail, 2)
	assertMarkersChronological(t, liveDetail, humanLiveChainTwoMarkers)
	assertMarkersAbsent(t, liveDetail, humanIntermediateMarkers...)
	activityX, activityY = findHumanActivityRowAt(t, page, 1)
	requireCue(t, "close chain-2 activity details", page.MouseClick(activityX, activityY))
	liveCollapsed = waitForHumanScreen(t, page, "closed chain-2 activity", func(screen string) bool {
		return strings.Contains(screen, "SecondUserVisibleMarker") && !strings.Contains(screen, "ChainTwoThinkingLiveMarker")
	})
	assertHumanActivityRowCount(t, liveCollapsed, 2)

	requireCue(t, "type chain-2 finish command", page.Type("/finish"))
	requireCue(t, "submit chain-2 finish command", page.Press("Enter"))
	waitHumanFixtureInput(t, humanDir, "/finish")
	finishedData := waitForHumanData(t, profile, sessionID, func(data *human.Data) bool {
		return len(data.Entries) == 6 &&
			data.Entries[4].ID == "activity-chain-2" &&
			data.Entries[4].Status == human.ActivityStatusDone &&
			data.Entries[5].ID == "answer-2" &&
			strings.Contains(data.Entries[5].Text, "SecondAnswerVisibleMarker")
	}, "committed chain-2 and second final answer")
	if finishedData.LiveRevision <= liveData.LiveRevision || countHumanActivities(finishedData) != 2 {
		t.Fatalf("finished revisions/activity count = %d/%d, want increasing live revision and two chains", finishedData.LiveRevision, countHumanActivities(finishedData))
	}
	finishedCollapsed := waitForHumanScreen(t, page, "finished two-chain conversation", func(screen string) bool {
		return strings.Contains(screen, "SecondUserVisibleMarker") && strings.Contains(screen, "SecondAnswerVisibleMarker")
	})
	assertHumanActivityRowCount(t, finishedCollapsed, 2)
	assertMarkersAbsent(t, finishedCollapsed, humanIntermediateMarkers...)
	assertMarkersAbsent(t, finishedCollapsed, humanLiveChainTwoMarkers...)

	focusHumanPTY(t, page)
	requireCue(t, "type native dialog command", page.Type("/settings"))
	waitHumanInput(t, profile, sessionID, func(input human.Input) bool {
		return strings.Contains(strings.Join(input.Lines, "\n"), "/settings")
	}, "native dialog command")
	requireCue(t, "open fake Pi native dialog", page.Press("Enter"))
	waitHumanFixtureInput(t, humanDir, "/settings")
	waitForHumanInputState(t, profile, sessionID, true)
	waitForHumanScreen(t, page, "native Pi dialog fallback", func(screen string) bool {
		return strings.Contains(screen, "FAKE_PI_NATIVE_DIALOG")
	})
	waitForHumanInputState(t, profile, sessionID, true)
	requireCue(t, "forward Escape to fake Pi dialog", page.Press("Escape"))
	waitForHumanScreen(t, page, "Human projection after native dialog", func(screen string) bool {
		return strings.Contains(screen, "UserVisibleMarker") && !strings.Contains(screen, "FAKE_PI_NATIVE_DIALOG") && humanButtonEnabled(screen)
	})
	waitForHumanInputState(t, profile, sessionID, false)

	buttonX, buttonY, enabled = findHumanButton(t, page)
	if !enabled {
		t.Fatal("Human toggle lost its enabled state after native dialog closed")
	}
	clickHuman(t, page, buttonX, buttonY)
	waitForHumanScreen(t, page, "native PTY after disabling Human", func(screen string) bool {
		return strings.Contains(screen, "FAKE_PI_EDITOR_ACTIVE") && !humanButtonEnabled(screen)
	})
	requireCue(t, "type into restored raw terminal", page.Type("/raw-live"))
	requireCue(t, "submit into restored raw terminal", page.Press("Enter"))
	if err := page.WaitFor("FAKE_PI_RECEIVED:/raw-live", humanScreenWait); err != nil {
		screen, _ := page.Text()
		t.Fatalf("native PTY stopped receiving input after Human was disabled: %v\n%s", err, screen)
	}
}

func TestHumanProjectionFallbacks(t *testing.T) {
	t.Run("corrupt history", func(t *testing.T) {
		page, sessionID, humanDir := launchHumanPTY(t, "human-e2e-corrupt", "corrupt-projection-chat")
		t.Cleanup(func() {
			if t.Failed() {
				if err := saveTestArtifact(page, "HumanCorruptFallback"); err != nil {
					t.Logf("save HumanCorruptFallback artifact: %v", err)
				}
			}
		})
		clickHumanButton(t, page, false)
		focusHumanPTY(t, page)
		requireCue(t, "type history corruption command", page.Type("/corrupt"))
		waitHumanInput(t, "human-e2e-corrupt", sessionID, func(input human.Input) bool {
			return strings.Contains(strings.Join(input.Lines, "\n"), "/corrupt")
		}, "history corruption command")
		requireCue(t, "publish corrupt history fixture", page.Press("Enter"))
		waitHumanFixtureInput(t, humanDir, "/corrupt")
		if _, err := human.ReadForProfile("human-e2e-corrupt", sessionID); err == nil {
			t.Fatal("fake Pi did not corrupt history.json")
		}
		screen := waitForHumanScreen(t, page, "corrupt-projection diagnostic and native PTY", func(screen string) bool {
			return strings.Contains(screen, "FAKE_PI_HISTORY_CORRUPTED") && strings.Contains(screen, "Human: decode history.json")
		})
		if strings.Contains(screen, "UserVisibleMarker") {
			t.Fatal("corrupt Human projection did not fall back to native PTY")
		}
	})

	t.Run("mismatched live revision", func(t *testing.T) {
		page, sessionID, humanDir := launchHumanPTY(t, "human-e2e-mismatch", "mismatched-projection-chat")
		t.Cleanup(func() {
			if t.Failed() {
				if err := saveTestArtifact(page, "HumanMismatchFallback"); err != nil {
					t.Logf("save HumanMismatchFallback artifact: %v", err)
				}
			}
		})
		clickHumanButton(t, page, false)
		focusHumanPTY(t, page)
		requireCue(t, "type live mismatch command", page.Type("/mismatch"))
		waitHumanInput(t, "human-e2e-mismatch", sessionID, func(input human.Input) bool {
			return strings.Contains(strings.Join(input.Lines, "\n"), "/mismatch")
		}, "live mismatch command")
		requireCue(t, "publish mismatched live fixture", page.Press("Enter"))
		waitHumanFixtureInput(t, humanDir, "/mismatch")
		if _, err := human.ReadForProfile("human-e2e-mismatch", sessionID); err == nil {
			t.Fatal("fake Pi did not publish a mismatched live revision")
		}
		screen := waitForHumanScreen(t, page, "mismatched-projection diagnostic and native PTY", func(screen string) bool {
			return strings.Contains(screen, "FAKE_PI_MISMATCH_PUBLISHED") && strings.Contains(screen, "Human: live.json historyRevision")
		})
		if strings.Contains(screen, "UserVisibleMarker") {
			t.Fatal("mismatched Human projection did not fall back to native PTY")
		}
	})

	t.Run("stale editor frame width", func(t *testing.T) {
		const profile = "human-e2e-stale-frame"
		page, sessionID, humanDir := launchHumanPTY(t, profile, "stale-editor-frame-chat")
		t.Cleanup(func() {
			if t.Failed() {
				if err := saveTestArtifact(page, "HumanStaleFrameFallback"); err != nil {
					t.Logf("save HumanStaleFrameFallback artifact: %v", err)
				}
			}
		})
		clickHumanButton(t, page, false)
		initialFrame := waitHumanInput(t, profile, sessionID, func(input human.Input) bool {
			return !input.Native && input.Width > 0
		}, "initial valid editor frame for stale-width fallback")
		focusHumanPTY(t, page)
		const staleFrameCommand = "/stale-frame"
		requireCue(t, "type stale editor frame command", page.Type(staleFrameCommand))
		waitHumanInput(t, profile, sessionID, func(input human.Input) bool {
			return strings.Contains(strings.Join(input.Lines, "\n"), staleFrameCommand)
		}, "stale editor frame command")
		requireCue(t, "publish stale editor frame", page.Press("Enter"))
		waitHumanFixtureInput(t, humanDir, staleFrameCommand)
		staleFrame := waitHumanInput(t, profile, sessionID, func(input human.Input) bool {
			return input.Revision > initialFrame.Revision && input.Width == initialFrame.Width+1 && !input.Native
		}, "intentionally stale editor width")
		if staleFrame.Width != initialFrame.Width+1 {
			t.Fatalf("stale editor frame width = %d, want %d", staleFrame.Width, initialFrame.Width+1)
		}
		screen := waitForHumanScreen(t, page, "native fallback for stale editor width", func(screen string) bool {
			return strings.Contains(screen, "FAKE_PI_STALE_FRAME") &&
				strings.Contains(screen, "Human: ожидание кадра редактора") &&
				humanButtonEnabled(screen)
		})
		if strings.Contains(screen, "UserVisibleMarker") {
			t.Fatal("stale editor frame did not fall back to the native PTY")
		}
	})

	t.Run("live cannot replace a chain closed by its answer", func(t *testing.T) {
		profile := "human-e2e-closed-chain"
		page, sessionID, humanDir := launchHumanPTY(t, profile, "closed-chain-live-chat")
		t.Cleanup(func() {
			if t.Failed() {
				if err := saveTestArtifact(page, "HumanClosedChainFallback"); err != nil {
					t.Logf("save HumanClosedChainFallback artifact: %v", err)
				}
			}
		})
		clickHumanButton(t, page, false)
		focusHumanPTY(t, page)
		requireCue(t, "type closed historical chain command", page.Type("/historical-live"))
		waitHumanInput(t, profile, sessionID, func(input human.Input) bool {
			return strings.Contains(strings.Join(input.Lines, "\n"), "/historical-live")
		}, "closed historical chain command")
		requireCue(t, "publish live update for closed historical chain", page.Press("Enter"))
		waitHumanFixtureInput(t, humanDir, "/historical-live")
		readErr := waitForHumanProjectionError(t, profile, sessionID, "closed historical chain")
		if !strings.Contains(readErr.Error(), "activity-chain-1") {
			t.Fatalf("closed-chain error omits the matched historical activity: %v", readErr)
		}
		screen := waitForHumanScreen(t, page, "closed-chain rejection and native PTY fallback", func(screen string) bool {
			return strings.Contains(screen, "FAKE_PI_CLOSED_HISTORICAL_LIVE") && strings.Contains(screen, "Human: merge live.json")
		})
		if strings.Contains(screen, "UserVisibleMarker") {
			t.Fatal("reader replaced an activity that already has a later final answer")
		}
	})
}

func TestHumanMainFamiliarTabsAndResize(t *testing.T) {
	profile := "human-e2e-tabs"
	chatName := "main-familiar-tabs-chat"
	mainSessionID := paths.ProfileSlug(profile) + "__" + slug.Slug(chatName)
	registryPath := paths.FamiliarsJSONLPath(profile, mainSessionID)
	if err := os.Remove(registryPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("remove stale fake familiar registry: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Remove(registryPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Errorf("remove fake familiar registry: %v", err)
		}
	})
	app, page, startedSessionID, _ := launchHumanPTYWithTree(t, profile, chatName, []sessionStateItem{{Name: chatName}})
	if startedSessionID != mainSessionID {
		t.Fatalf("Main session ID = %q, want %q", startedSessionID, mainSessionID)
	}

	clickHumanButton(t, page, false)
	mainScreen := waitForHumanScreen(t, page, "Main Human projection", func(screen string) bool {
		return strings.Contains(screen, "MainOnlyUserMarker") && humanButtonEnabled(screen)
	})
	assertOneHumanActivityRow(t, mainScreen)

	familiarID := mainSessionID + "__fixture-familiar"
	writeFakeFamiliarRegistry(t, profile, mainSessionID, familiarID)
	switchChatTab(t, page, "fixture-familiar", "fake familiar PTY", func(screen string) bool {
		return strings.Contains(screen, "FAKE_PI_READY") && strings.Contains(screen, "session="+familiarID)
	})
	clickHumanButton(t, page, false)
	familiarScreen := waitForHumanScreen(t, page, "familiar Human projection", func(screen string) bool {
		return strings.Contains(screen, "FamiliarOnlyUserMarker") && humanButtonEnabled(screen)
	})
	assertOneHumanActivityRow(t, familiarScreen)
	if strings.Contains(familiarScreen, "MainOnlyUserMarker") {
		t.Fatal("Main projection leaked into the familiar tab")
	}
	familiarData, err := human.ReadForProfile(profile, familiarID)
	if err != nil {
		t.Fatalf("read fake familiar projection: %v", err)
	}
	if familiarData.Epoch == "" || len(familiarData.Entries) == 0 || !strings.Contains(familiarData.Entries[0].Text, "FamiliarOnlyUserMarker") {
		t.Fatalf("familiar tab read another session's projection: %#v", familiarData.Entries)
	}

	mainScreen = switchChatTab(t, page, "Main", "Main Human projection after familiar switch", func(screen string) bool {
		return strings.Contains(screen, "MainOnlyUserMarker") && humanButtonEnabled(screen)
	})
	if strings.Contains(mainScreen, "FamiliarOnlyUserMarker") {
		t.Fatal("familiar projection leaked into Main tab")
	}
	familiarScreen = switchChatTab(t, page, "fixture-familiar", "familiar Human state after tab switch", func(screen string) bool {
		return strings.Contains(screen, "FamiliarOnlyUserMarker") && humanButtonEnabled(screen)
	})
	if strings.Contains(familiarScreen, "MainOnlyUserMarker") {
		t.Fatal("switching tabs mixed Main and familiar Human views")
	}

	inputBeforeResize := familiarData.Input
	requireCue(t, "resize the active Human PTY", app.Resize(125, 42))
	inputAfterResize := waitHumanInput(t, profile, familiarID, func(input human.Input) bool {
		return !input.Native && input.Revision > inputBeforeResize.Revision && input.Width != inputBeforeResize.Width
	}, "familiar editor frame at resized PTY geometry")
	if inputAfterResize.Width == inputBeforeResize.Width {
		t.Fatalf("familiar editor-frame width did not change after resize: width=%d revision=%d", inputAfterResize.Width, inputAfterResize.Revision)
	}
	familiarResizedScreen := waitForHumanScreen(t, page, "familiar Human projection recovered after resize", func(screen string) bool {
		return strings.Contains(screen, "FamiliarOnlyUserMarker") && humanButtonEnabled(screen)
	})
	assertOneHumanActivityRow(t, familiarResizedScreen)
	if strings.Contains(familiarResizedScreen, "MainOnlyUserMarker") {
		t.Fatal("resized familiar Human view rendered Main projection data")
	}

	focusHumanPTY(t, page)
	const resizeInput = "/after-resize"
	requireCue(t, "type through the resized Human PTY", page.Type(resizeInput))
	waitHumanInput(t, profile, familiarID, func(input human.Input) bool {
		return strings.Contains(strings.Join(input.Lines, "\n"), resizeInput)
	}, "draft through resized Human PTY")
	requireCue(t, "submit through the resized Human PTY", page.Press("Enter"))
	waitHumanFixtureInput(t, human.Directory(profile, familiarID), resizeInput)
	waitForHumanScreen(t, page, "familiar Human projection after resized PTY input", func(screen string) bool {
		return strings.Contains(screen, "FamiliarOnlyUserMarker") && humanButtonEnabled(screen)
	})
}

func TestHumanHideLifecycle(t *testing.T) {
	profile := "human-e2e-hide"
	chatName := "hide-lifecycle-chat"
	folderName := "hide-lifecycle-folder"
	_, page, sessionID, _ := launchHumanPTYWithTree(t, profile, chatName, []sessionStateItem{
		{Name: chatName},
		{Name: folderName, IsFolder: true, Expanded: true},
	})
	clickHumanButton(t, page, false)
	visible := waitForHumanScreen(t, page, "Human before hiding its ChatPanel", func(screen string) bool {
		return strings.Contains(screen, "MainOnlyUserMarker") && humanButtonEnabled(screen)
	})
	assertOneHumanActivityRow(t, visible)

	requireCue(t, "focus tree before hiding the chat panel", page.Press("F6"))
	requireCue(t, "select folder to hide the Human ChatPanel", page.Press("End"))
	requireCue(t, "hide Human ChatPanel by opening a folder", page.Press("Enter"))
	hidden := waitForHumanScreen(t, page, "folder view after hiding Human", func(screen string) bool {
		return strings.Contains(screen, folderName) && !strings.Contains(screen, "MainOnlyUserMarker")
	})
	if strings.Contains(hidden, " × Clear ") || strings.Contains(hidden, "Human") {
		t.Fatalf("hidden ChatPanel controls or Human content remained visible:\n%s", hidden)
	}

	requireCue(t, "focus tree to restore the chat panel", page.Press("F6"))
	requireCue(t, "select Main to restore its ChatPanel", page.Press("Home"))
	requireCue(t, "restore the Main chat after folder view", page.Press("Enter"))
	waitForHumanScreen(t, page, "restored native Main PTY", func(screen string) bool {
		return strings.Contains(screen, "FAKE_PI_READY") && strings.Contains(screen, "session="+sessionID)
	})
	clickHumanButton(t, page, false)
	restored := waitForHumanScreen(t, page, "Human projection after ChatPanel reactivation", func(screen string) bool {
		return strings.Contains(screen, "MainOnlyUserMarker") && humanButtonEnabled(screen)
	})
	assertOneHumanActivityRow(t, restored)
}

func buildHumanFakePi(t *testing.T) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "fake-human-pi")
	command := exec.Command("go", "build", "-trimpath", "-buildvcs=false", "-o", binary, "./e2e/testdata/human-pi")
	command.Dir = projectRoot()
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("build fake Human Pi: %v\n%s", err, output)
	}
	return binary
}

type fakeFamiliarRegistryEntry struct {
	ID        string `json:"id"`
	SessionID string `json:"sessionId"`
	Created   string `json:"created"`
}

func launchHumanPTY(t *testing.T, profile, chatName string) (*cue.Page, string, string) {
	t.Helper()
	_, page, sessionID, humanDir := launchHumanPTYWithTree(t, profile, chatName, []sessionStateItem{{Name: chatName}})
	return page, sessionID, humanDir
}

func launchHumanPTYWithTree(t *testing.T, profile, chatName string, items []sessionStateItem) (*cue.App, *cue.Page, string, string) {
	t.Helper()
	writeSessionFixture(t, profile, items)
	sessionID := paths.ProfileSlug(profile) + "__" + slug.Slug(chatName)
	fakePi := buildHumanFakePi(t)
	app, err := cue.Launch(automataBinary(t),
		cue.WithArgs("--profile", profile),
		cue.WithDir(projectRoot()),
		cue.WithSize(160, 55),
		cue.WithEnv("TERM=xterm-256color", "PI_SKIP_VERSION_CHECK=1", "PI_CMD="+fakePi),
	)
	if err != nil {
		t.Fatalf("launch isolated Automata: %v", err)
	}
	t.Cleanup(func() { closeCueApp(t, app) })
	page := app.Page()
	page.WaitStable(100 * time.Millisecond)
	requireCue(t, "enable terminal mouse", page.EnableMouse())
	if err := page.WaitFor(chatName, 5*time.Second); err != nil {
		screen, _ := page.Text()
		t.Fatalf("isolated chat %q was not rendered: %v\n%s", chatName, err, screen)
	}
	chatX, chatY := findRenderedText(t, page, chatName)
	requireCue(t, "open isolated chat", page.MouseClick(chatX, chatY))
	ready := "FAKE_PI_READY"
	if err := page.WaitFor(ready, 8*time.Second); err != nil {
		screen, _ := page.Text()
		t.Fatalf("fake Pi did not start with --session-id %q: %v\n%s", sessionID, err, screen)
	}
	humanDir := human.Directory(paths.ProfileSlug(profile), sessionID)
	if humanDir == "" {
		t.Fatalf("invalid fixture identity profile=%q session=%q", profile, sessionID)
	}
	return app, page, sessionID, humanDir
}

func writeFakeFamiliarRegistry(t *testing.T, profile, ownerSessionID, familiarSessionID string) {
	t.Helper()
	if err := paths.ValidateFamiliarSessionID(ownerSessionID, familiarSessionID); err != nil {
		t.Fatalf("validate fake familiar session: %v", err)
	}
	registryPath := paths.FamiliarsJSONLPath(profile, ownerSessionID)
	if err := os.MkdirAll(filepath.Dir(registryPath), 0o700); err != nil {
		t.Fatalf("create fake familiar registry directory: %v", err)
	}
	registry, err := json.Marshal([]fakeFamiliarRegistryEntry{{
		ID:        "fixture-familiar",
		SessionID: familiarSessionID,
		Created:   "isolated-e2e-fixture",
	}})
	if err != nil {
		t.Fatalf("encode fake familiar registry: %v", err)
	}
	if err := os.WriteFile(registryPath, registry, 0o600); err != nil {
		t.Fatalf("write fake familiar registry: %v", err)
	}
}

func verifyHumanFixtureProjection(t *testing.T, profile, sessionID, directory string) {
	t.Helper()
	info, err := os.Stat(directory)
	if err != nil {
		t.Fatalf("inspect fake Pi Human directory: %v", err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("Human directory permissions = %04o, want 0700", info.Mode().Perm())
	}
	for _, name := range []string{"history.json", "live.json", "input.json"} {
		fileInfo, err := os.Stat(filepath.Join(directory, name))
		if err != nil {
			t.Fatalf("inspect fake Pi %s: %v", name, err)
		}
		if fileInfo.Mode().Perm() != 0o600 {
			t.Fatalf("fake Pi %s permissions = %04o, want 0600", name, fileInfo.Mode().Perm())
		}
	}
	data, err := human.ReadForProfile(profile, sessionID)
	if err != nil {
		t.Fatalf("read fake Pi Human v1 projection: %v", err)
	}
	if data.Epoch == "" || len(data.Entries) != 3 {
		t.Fatalf("fake Pi projection epoch/entries = %q/%d, want nonempty epoch and 3 entries", data.Epoch, len(data.Entries))
	}
	activity := data.Entries[1]
	if activity.Kind != human.EntryKindActivity || activity.ID != "activity-chain-1" || activity.Status != human.ActivityStatusDone {
		t.Fatalf("initial activity = %#v", activity)
	}
	toolCalls := 0
	for _, item := range activity.Items {
		if item.Kind == human.ItemKindToolCall {
			toolCalls++
		}
	}
	if toolCalls != 2 || len(activity.Items) != 7 {
		t.Fatalf("initial activity has %d items and %d tool calls, want 7 items and 2 tool calls", len(activity.Items), toolCalls)
	}
	if data.Input.Width < 1 || data.Input.Native {
		t.Fatalf("initial input frame = %#v, want positive PTY width and native=false", data.Input)
	}
}

func assertProjectionIsolation(t *testing.T, profile, sessionID string) {
	t.Helper()
	for _, candidate := range []struct {
		profile   string
		sessionID string
	}{
		{profile: profile, sessionID: profile + "__other-chat"},
		{profile: profile + "-other", sessionID: profile + "-other__human-pty-chat"},
	} {
		path := human.Directory(candidate.profile, candidate.sessionID)
		if path == "" {
			t.Fatalf("invalid isolation candidate profile=%q session=%q", candidate.profile, candidate.sessionID)
		}
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("fixture unexpectedly created another profile/session projection %q (err=%v; main=%q)", path, err, sessionID)
		}
	}
}

func findHumanButton(t *testing.T, page *cue.Page) (int, int, bool) {
	t.Helper()
	lines, err := page.Lines()
	if err != nil {
		t.Fatalf("read Human tab bar: %v", err)
	}
	for row, line := range lines {
		humanIndex := strings.LastIndex(line, "Human")
		clearIndex := strings.LastIndex(line, "Clear")
		if humanIndex < 0 || clearIndex < 0 {
			continue
		}
		humanColumn := ansi.StringWidth(line[:humanIndex])
		clearColumn := ansi.StringWidth(line[:clearIndex])
		if humanColumn >= clearColumn {
			t.Fatalf("Human button is not left of Clear: Human=%d Clear=%d in %q", humanColumn, clearColumn, line)
		}
		return humanColumn, row, strings.Contains(line, "● Human")
	}
	t.Fatalf("Human button beside Clear is missing:\n%s", strings.Join(lines, "\n"))
	return 0, 0, false
}

func humanButtonEnabled(screen string) bool {
	for _, line := range strings.Split(screen, "\n") {
		if strings.Contains(line, "Clear") && strings.Contains(line, "Human") {
			return strings.Contains(line, "● Human")
		}
	}
	return false
}

func focusHumanPTY(t *testing.T, page *cue.Page) {
	t.Helper()
	requireCue(t, "focus tree before cycling to chat PTY", page.Press("F6"))
	requireCue(t, "focus chat PTY", page.Press("F7"))
	page.WaitStable(100 * time.Millisecond)
}

func clickHumanButton(t *testing.T, page *cue.Page, enabled bool) {
	t.Helper()
	x, y, currentlyEnabled := findHumanButton(t, page)
	if currentlyEnabled != enabled {
		t.Fatalf("Human enabled = %v, want %v", currentlyEnabled, enabled)
	}
	clickHuman(t, page, x, y)
}

func clickHuman(t *testing.T, page *cue.Page, x, y int) {
	t.Helper()
	wantEnabled := !humanButtonEnabledFromPage(page)
	requireCue(t, "click Human toggle", page.MouseClick(x, y))
	waitForHumanScreen(t, page, "Human toggle state", func(screen string) bool {
		return humanButtonEnabled(screen) == wantEnabled
	})
}

func humanButtonEnabledFromPage(page *cue.Page) bool {
	screen, err := page.Text()
	if err != nil {
		return false
	}
	return humanButtonEnabled(screen)
}

func findRenderedText(t *testing.T, page *cue.Page, needle string) (int, int) {
	t.Helper()
	lines, err := page.Lines()
	if err != nil {
		t.Fatalf("read screen for %q: %v", needle, err)
	}
	for row, line := range lines {
		if byteColumn := strings.Index(line, needle); byteColumn >= 0 {
			return ansi.StringWidth(line[:byteColumn]), row
		}
	}
	t.Fatalf("rendered text %q is missing:\n%s", needle, strings.Join(lines, "\n"))
	return 0, 0
}

func switchChatTab(t *testing.T, page *cue.Page, name, description string, condition func(string) bool) string {
	t.Helper()
	deadline := time.Now().Add(humanScreenWait)
	var screen string
	for time.Now().Before(deadline) {
		current, err := page.Text()
		if err != nil {
			t.Fatalf("read screen while switching to %s: %v", description, err)
		}
		screen = current
		if condition(screen) {
			return screen
		}
		x, y, found := chatTabPosition(page, name)
		if found {
			if err := page.MouseClick(x, y); err != nil {
				t.Fatalf("click %s tab: %v", name, err)
			}
			page.WaitStable(100 * time.Millisecond)
			continue
		}
		time.Sleep(40 * time.Millisecond)
	}
	t.Fatalf("timed out switching to %s:\n%s", description, screen)
	return screen
}

func chatTabPosition(page *cue.Page, name string) (int, int, bool) {
	lines, err := page.Lines()
	if err != nil {
		return 0, 0, false
	}
	for row, line := range lines {
		if !strings.Contains(line, "Clear") {
			continue
		}
		if byteColumn := strings.Index(line, name); byteColumn >= 0 {
			return ansi.StringWidth(line[:byteColumn]) + ansi.StringWidth(name)/2, row, true
		}
	}
	return 0, 0, false
}

func humanActivityRows(screen string) []int {
	var rows []int
	for row, line := range strings.Split(screen, "\n") {
		if strings.Contains(line, "Активность") || strings.Contains(line, "Размышления и инструменты") {
			rows = append(rows, row)
		}
	}
	return rows
}

func findHumanActivityRow(t *testing.T, page *cue.Page) (int, int) {
	return findHumanActivityRowAt(t, page, 0)
}

func findHumanActivityRowAt(t *testing.T, page *cue.Page, target int) (int, int) {
	t.Helper()
	lines, err := page.Lines()
	if err != nil {
		t.Fatalf("read activity chain row: %v", err)
	}
	var coordinates [][2]int
	for row, line := range lines {
		byteColumn := strings.Index(line, "Активность")
		if byteColumn < 0 {
			byteColumn = strings.Index(line, "Размышления и инструменты")
		}
		if byteColumn >= 0 {
			coordinates = append(coordinates, [2]int{ansi.StringWidth(line[:byteColumn]), row})
		}
	}
	if target < 0 || target >= len(coordinates) {
		t.Fatalf("activity row index %d missing; found %d rows:\n%s", target, len(coordinates), strings.Join(lines, "\n"))
	}
	return coordinates[target][0], coordinates[target][1]
}

func assertOneHumanActivityRow(t *testing.T, screen string) {
	assertHumanActivityRowCount(t, screen, 1)
}

func assertHumanActivityRowCount(t *testing.T, screen string, want int) {
	t.Helper()
	if rows := humanActivityRows(screen); len(rows) != want {
		t.Fatalf("collapsed Human projection has %d activity rows, want %d:\n%s", len(rows), want, screen)
	}
}

func assertMarkersAbsent(t *testing.T, screen string, markers ...string) {
	t.Helper()
	for _, marker := range markers {
		if strings.Contains(screen, marker) {
			t.Fatalf("intermediate marker %q leaked into collapsed Human view:\n%s", marker, screen)
		}
	}
}

func assertMarkersChronological(t *testing.T, screen string, markers []string) {
	t.Helper()
	previous := -1
	for _, marker := range markers {
		position := strings.Index(screen, marker)
		if position < 0 {
			t.Fatalf("expanded activity omitted %q:\n%s", marker, screen)
		}
		if position <= previous {
			t.Fatalf("expanded activity order changed at %q:\n%s", marker, screen)
		}
		previous = position
	}
}

func assertLiveMarkersBelongToSecondChain(t *testing.T, data *human.Data, markers []string) {
	t.Helper()
	if data == nil {
		t.Fatal("Human data is nil")
	}
	for _, marker := range markers {
		foundInChainTwo := false
		for _, entry := range data.Entries {
			found := strings.Contains(entry.Text, marker)
			for _, item := range entry.Items {
				found = found || strings.Contains(item.Text, marker)
			}
			if !found {
				continue
			}
			if entry.ID != "activity-chain-2" || entry.Kind != human.EntryKindActivity {
				t.Fatalf("live marker %q appeared outside chain-2 in entry %#v", marker, entry)
			}
			foundInChainTwo = true
		}
		if !foundInChainTwo {
			t.Fatalf("live marker %q is missing from chain-2", marker)
		}
	}
}

func countHumanActivities(data *human.Data) int {
	if data == nil {
		return 0
	}
	count := 0
	for _, entry := range data.Entries {
		if entry.Kind == human.EntryKindActivity {
			count++
		}
	}
	return count
}

func waitForHumanScreen(t *testing.T, page *cue.Page, description string, condition func(string) bool) string {
	t.Helper()
	deadline := time.Now().Add(humanScreenWait)
	var screen string
	for time.Now().Before(deadline) {
		var err error
		screen, err = page.Text()
		if err != nil {
			t.Fatalf("read screen while waiting for %s: %v", description, err)
		}
		if condition(screen) {
			return screen
		}
		time.Sleep(40 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s:\n%s", description, screen)
	return screen
}

func waitHumanInput(t *testing.T, profile, sessionID string, condition func(human.Input) bool, description string) human.Input {
	t.Helper()
	deadline := time.Now().Add(humanScreenWait)
	var last human.Input
	for time.Now().Before(deadline) {
		data, err := human.ReadForProfile(profile, sessionID)
		if err == nil && data != nil {
			last = data.Input
			if condition(data.Input) {
				return data.Input
			}
		}
		time.Sleep(40 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for Human input frame %s; last frame=%#v", description, last)
	return human.Input{}
}

func waitForHumanData(t *testing.T, profile, sessionID string, condition func(*human.Data) bool, description string) *human.Data {
	t.Helper()
	deadline := time.Now().Add(humanScreenWait)
	for time.Now().Before(deadline) {
		data, err := human.ReadForProfile(profile, sessionID)
		if err == nil && data != nil && condition(data) {
			return data
		}
		time.Sleep(40 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for Human projection %s", description)
	return nil
}

func waitForHumanProjectionError(t *testing.T, profile, sessionID, contains string) error {
	t.Helper()
	deadline := time.Now().Add(humanScreenWait)
	for time.Now().Before(deadline) {
		_, err := human.ReadForProfile(profile, sessionID)
		if err != nil && strings.Contains(err.Error(), contains) {
			return err
		}
		time.Sleep(40 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for Human reader error containing %q", contains)
	return nil
}

func waitForHumanInputState(t *testing.T, profile, sessionID string, native bool) {
	t.Helper()
	waitHumanInput(t, profile, sessionID, func(input human.Input) bool {
		return input.Native == native
	}, fmt.Sprintf("native=%v", native))
}

func waitHumanFixtureInput(t *testing.T, directory, expected string) {
	t.Helper()
	path := filepath.Join(directory, "fixture-input.log")
	deadline := time.Now().Add(humanScreenWait)
	var contents string
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path)
		if err == nil {
			contents = string(data)
			if strings.Contains(contents, expected) {
				return
			}
		}
		time.Sleep(40 * time.Millisecond)
	}
	t.Fatalf("fake Pi did not receive input %q through PTY; log=%q", expected, contents)
}
