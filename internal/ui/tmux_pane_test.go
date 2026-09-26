package ui

import (
	"reflect"
	"testing"
	"unsafe"

	"github.com/HumanHorizon/automata/internal/scrollback"
	"github.com/Starframe/portalis"
)

func TestTmuxPaneUsesConfigurableScrollbackOnItsScreen(t *testing.T) {
	panel := NewTmuxPanePanel("%1")
	if panel.scrollbackLimit != scrollback.DefaultLines {
		t.Fatalf("default tmux scrollback = %d, want %d", panel.scrollbackLimit, scrollback.DefaultLines)
	}
	if err := panel.SetScrollbackLimit(0); err != nil {
		t.Fatal(err)
	}
	if panel.scrollbackLimit != 0 {
		t.Fatalf("configured tmux scrollback = %d, want unlimited", panel.scrollbackLimit)
	}
	if err := panel.SetScrollbackLimit(-1); err == nil {
		t.Fatal("negative tmux scrollback limit was accepted")
	}
	if panel.scrollbackLimit != 0 {
		t.Fatalf("invalid setting changed tmux scrollback to %d", panel.scrollbackLimit)
	}

	panel.screen = portalis.NewScreen(24, 80)
	if err := panel.SetScrollbackLimit(0); err != nil {
		t.Fatal(err)
	}
	limitField := reflect.ValueOf(panel.screen).Elem().FieldByName("scrollbackLimit")
	if got := reflect.NewAt(limitField.Type(), unsafe.Pointer(limitField.UnsafeAddr())).Elem().Int(); got != 0 {
		t.Fatalf("tmux Screen scrollback limit = %d, want unlimited", got)
	}
}

func TestTmuxPaneConstructorRejectsNegativeScrollback(t *testing.T) {
	if _, err := NewTmuxPanePanelWithScrollbackLimit("%1", -1); err == nil {
		t.Fatal("negative constructor scrollback limit was accepted")
	}
	panel, err := NewTmuxPanePanelWithScrollbackLimit("%1", 0)
	if err != nil {
		t.Fatal(err)
	}
	if panel.scrollbackLimit != 0 {
		t.Fatalf("constructor scrollback = %d, want unlimited", panel.scrollbackLimit)
	}
}
