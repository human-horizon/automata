package tree

import "strings"

// KeyboardHelpText returns the shared keyboard navigation help content.
func KeyboardHelpText() string {
	return strings.Join([]string{
		"Фокус",
		"F5 / F7    предыдущая / следующая панель",
		"F6         вернуть фокус дереву",
		"",
		"Дерево",
		"↑ / ↓      выбрать элемент",
		"← / →      свернуть, раскрыть или перейти",
		"Home/End   первый или последний элемент",
		"PgUp/PgDn  перемещение на экран",
		"Enter      открыть или раскрыть",
		"F2         переименовать",
		"F10        контекстное меню",
		"",
		"F1         показать эту справку",
		"Esc        закрыть меню или справку",
		"",
		"Ctrl+C       завершить Automata",
		"Alt, Tab и Shift+Tab в чате передаются Pi/zellij.",
	}, "\n")
}

func keyboardHelpText() string {
	return KeyboardHelpText()
}
