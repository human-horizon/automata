package ui

import (
	"bytes"
	"strconv"
	"testing"

	"github.com/Starframe/portalis"
)

func BenchmarkTerminalScrollbackLines(b *testing.B) {
	line := append(bytes.Repeat([]byte("x"), 79), '\n')
	workload := bytes.Repeat(line, 5000)
	for _, limit := range []int{100, 300, 500, 1000} {
		b.Run(strconv.Itoa(limit), func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				screen := portalis.NewScreen(24, 80)
				screen.SetScrollbackLimit(limit)
				parser := portalis.NewParser(screen)
				parser.Feed(workload)
				_ = screen.Render()
			}
		})
	}
}
