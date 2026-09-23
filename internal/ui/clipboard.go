package ui

import atottoclipboard "github.com/atotto/clipboard"

type notesClipboard interface {
	Read() (string, error)
	Write(string) error
}

type systemClipboard struct{}

func newSystemClipboard() notesClipboard {
	return systemClipboard{}
}

func (systemClipboard) Read() (string, error) {
	return atottoclipboard.ReadAll()
}

func (systemClipboard) Write(text string) error {
	return atottoclipboard.WriteAll(text)
}
