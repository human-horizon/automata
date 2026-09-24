package paths

import "testing"

func TestValidateSessionID(t *testing.T) {
	tests := []struct {
		name      string
		sessionID string
		wantErr   bool
	}{
		{name: "default chat", sessionID: "chat"},
		{name: "profile and dotted folders", sessionID: "humanhorizon__folder.child-chat"},
		{name: "unicode letters", sessionID: "профиль__чат"},
		{name: "empty", sessionID: "", wantErr: true},
		{name: "traversal", sessionID: "../chat", wantErr: true},
		{name: "embedded traversal", sessionID: "chat..other", wantErr: true},
		{name: "slash", sessionID: "chat/other", wantErr: true},
		{name: "backslash", sessionID: "chat\\other", wantErr: true},
		{name: "absolute path", sessionID: "/tmp/chat", wantErr: true},
		{name: "drive path", sessionID: "C:chat", wantErr: true},
		{name: "control character", sessionID: "chat\nother", wantErr: true},
		{name: "dot", sessionID: ".", wantErr: true},
		{name: "leading dot", sessionID: ".chat", wantErr: true},
		{name: "trailing dot", sessionID: "chat.", wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateSessionID(test.sessionID)
			if (err != nil) != test.wantErr {
				t.Fatalf("ValidateSessionID(%q) error = %v, wantErr=%v", test.sessionID, err, test.wantErr)
			}
		})
	}
}

func TestValidateFamiliarSessionID(t *testing.T) {
	const owner = "humanhorizon__folder.child"
	for _, familiarID := range []string{owner + "__expert", owner + "__expert-2"} {
		if err := ValidateFamiliarSessionID(owner, familiarID); err != nil {
			t.Errorf("ValidateFamiliarSessionID(%q, %q): %v", owner, familiarID, err)
		}
	}
	for _, familiarID := range []string{
		owner,
		owner + "__",
		"other__expert",
		owner + "__/../outside",
	} {
		if err := ValidateFamiliarSessionID(owner, familiarID); err == nil {
			t.Errorf("ValidateFamiliarSessionID(%q, %q) unexpectedly succeeded", owner, familiarID)
		}
	}
}
