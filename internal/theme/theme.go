// Package theme defines the visual themes available in Automata.
package theme

// DinosaurEarthSunnyGrassyID is the stable identifier of the first Automata theme.
const DinosaurEarthSunnyGrassyID = "dinosaur-earth-sunny-grassy"

// Theme contains semantic colors used by native Automata panels.
type Theme struct {
	ID   string
	Name string

	Background  string
	Surface     string
	Raised      string
	Border      string
	BorderMuted string

	Text       string
	TextStrong string
	TextMuted  string
	TextDim    string

	Pink        string
	PinkMuted   string
	Lime        string
	LimeMuted   string
	Purple      string
	PurpleMuted string

	Error   string
	Warning string
	Success string

	SelectionBackground string
	SelectionForeground string
}

var available = []Theme{
	{
		ID:   DinosaurEarthSunnyGrassyID,
		Name: "Dinosaur Earth · Sunny Grassy",

		Background:  "#302c29",
		Surface:     "#463f3b",
		Raised:      "#5d514b",
		Border:      "#75645b",
		BorderMuted: "#514640",

		Text:       "#decfc8",
		TextStrong: "#f4eae4",
		TextMuted:  "#baa9a0",
		TextDim:    "#9b8d85",

		Pink:        "#f08cae",
		PinkMuted:   "#b86686",
		Lime:        "#b7e35b",
		LimeMuted:   "#7f9e45",
		Purple:      "#c28cff",
		PurpleMuted: "#8e6caf",

		Error:   "#c9695e",
		Warning: "#d9ee67",
		Success: "#b7e35b",

		SelectionBackground: "#665750",
		SelectionForeground: "#f4eae4",
	},
}

// All returns the available themes in stable display order.
func All() []Theme {
	return append([]Theme(nil), available...)
}

// Default returns the first available theme.
func Default() Theme {
	return available[0]
}

// ByID resolves a persisted theme identifier.
func ByID(id string) (Theme, bool) {
	for _, item := range available {
		if item.ID == id {
			return item, true
		}
	}
	return Theme{}, false
}

// Resolve returns the requested theme or the default when the identifier is unknown.
func Resolve(id string) Theme {
	if item, ok := ByID(id); ok {
		return item
	}
	return Default()
}
