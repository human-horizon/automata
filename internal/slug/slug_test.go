package slug

import "testing"

func TestSlug(t *testing.T) {
	cases := []struct {
		input    string
		expected string
	}{
		{"Hello World", "hello-world"},
		{"Проекты", "proekty"},
		{"My_Chat", "my-chat"},
		{"Chat.Name", "chat-name"},
		{"Folder/Sub", "folder-sub"},
		{"Chat#1", "chat-1"},
		{"---a--b---", "a-b"},
		{"  spaced  ", "spaced"},
		{"UPPER", "upper"},
		{"Ёжик", "yozhik"},
	}

	for _, c := range cases {
		got := Slug(c.input)
		if got != c.expected {
			t.Errorf("Slug(%q) = %q, want %q", c.input, got, c.expected)
		}
	}
}

func TestComposeSessionID(t *testing.T) {
	cases := []struct {
		cwd      string
		folders  []string
		chatName string
		expected string
	}{
		{
			cwd:      "/Users/a/Projects",
			folders:  []string{"Проекты"},
			chatName: "My Chat",
			expected: "proekty.my-chat",
		},
		{
			cwd:      "/home/user",
			folders:  nil,
			chatName: "General",
			expected: "general",
		},
		{
			cwd:      "/tmp",
			folders:  []string{"Folder/One", "Two.Three"},
			chatName: "Chat#Name",
			expected: "folder-one.two-three.chat-name",
		},
	}

	for _, c := range cases {
		got := ComposeSessionID(c.cwd, c.folders, c.chatName)
		if got != c.expected {
			t.Errorf("ComposeSessionID(%q, %q, %q) = %q, want %q",
				c.cwd, c.folders, c.chatName, got, c.expected)
		}
	}
}

func TestSessionName(t *testing.T) {
	cases := []struct {
		folders  []string
		chatName string
		expected string
	}{
		{[]string{"Проекты"}, "My Chat", "proekty.my-chat"},
		{nil, "General", "general"},
		{[]string{"Работа", "Проекты"}, "brainstorming", "rabota.proekty.brainstorming"},
		{[]string{"Work"}, "Обсуждение", "work.obsuzhdenie"},
		{[]string{"Folder/One", "Two.Three"}, "Chat#Name", "folder-one.two-three.chat-name"},
	}

	for _, c := range cases {
		got := SessionName(c.folders, c.chatName)
		if got != c.expected {
			t.Errorf("SessionName(%q, %q) = %q, want %q",
				c.folders, c.chatName, got, c.expected)
		}
	}
}
