package ui

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
)

// stackedTable receives an already sanitized grid. Labels and values wrap
// together, so even an unusually long header cannot overflow the terminal.
func stackedTable(w io.Writer, headers []string, grid [][]string, mode Mode) error {
	var labels []string
	if len(headers) > 0 && len(grid) > 1 {
		labels, grid = grid[0], grid[1:]
	}
	var b bytes.Buffer
	for i, row := range grid {
		if i > 0 {
			b.WriteByte('\n')
		}
		for j, cell := range row {
			if cell == "" {
				continue
			}
			text := cell
			if j < len(labels) && labels[j] != "" {
				text = labels[j] + ": " + cell
			}
			for _, line := range wrapLine(text, mode.Width) {
				b.WriteString(line)
				b.WriteByte('\n')
			}
		}
	}
	_, err := w.Write(b.Bytes())
	return err
}

// wrapLine prefers word boundaries, but splits long identifiers rather than
// losing their suffix. At a one-column width a wide rune is replaced by "?"
// because no representation of that rune can fit in the available space.
func wrapLine(text string, width int) []string {
	var lines []string
	for text != "" {
		if DisplayWidth(text) <= width {
			lines = append(lines, text)
			break
		}
		end, columns, space := 0, 0, -1
		for i, r := range text {
			if columns+runeWidth(r) > width {
				break
			}
			columns += runeWidth(r)
			end = i + len(string(r))
			if r == ' ' {
				space = i
			}
		}
		if end == 0 {
			_, size := utf8.DecodeRuneInString(text)
			lines = append(lines, "?")
			text = text[size:]
			continue
		}
		if space > 0 {
			end = space
		}
		lines = append(lines, strings.TrimRight(text[:end], " "))
		text = strings.TrimLeft(text[end:], " ")
	}
	return lines
}

// EmptyState prints a successful empty result and one recovery hint. It is
// for human output only; commands keep their existing empty JSON collections.
// Callers use the result stream, not the diagnostic stream.
func EmptyState(w io.Writer, message, hint string) error {
	text := SanitizeLine(message) + "\n"
	if hint != "" {
		text += "hint: " + SanitizeLine(hint) + "\n"
	}
	_, err := fmt.Fprint(w, text)
	return err
}
