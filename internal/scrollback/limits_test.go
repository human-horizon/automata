package scrollback

import "testing"

func TestValidateLimit(t *testing.T) {
	for _, value := range []int{0, DefaultLines, 1000} {
		if err := Validate(value); err != nil {
			t.Errorf("Validate(%d) = %v, want nil", value, err)
		}
	}
	if err := Validate(-1); err == nil {
		t.Fatal("negative scrollback limit was accepted")
	}
}
