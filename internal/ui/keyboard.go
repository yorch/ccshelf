package ui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/term"
)

// The picker owns no background reader: polling allows cancellation without
// leaving a read behind that could consume Claude's input after restoration.
var (
	pickerMakeRaw = term.MakeRaw
	pickerRestore = term.Restore
	pickerSize    = term.GetSize
	pickerRead    = readPickerByte
	pickerOutput  = preparePickerOutput
)

const pickerPoll = 25 * time.Millisecond

type pickerKey struct {
	kind byte
	text rune
}

const (
	keyText byte = iota
	keyUp
	keyDown
	keyEnter
	keySpace
	keyBackspace
	keyReset
	keyCancel
	keyIgnore
)

// keyParser incrementally decodes UTF-8 and bounded CSI/SS3 key sequences.
// Incomplete Escape is canceled after a short idle timeout by the reader.
type keyParser struct{ pending []byte }

func (p *keyParser) feed(b byte) (pickerKey, bool) {
	p.pending = append(p.pending, b)
	if p.pending[0] == 27 {
		if len(p.pending) == 1 {
			return pickerKey{}, false
		}
		if len(p.pending) == 2 && (b == '[' || b == 'O') {
			return pickerKey{}, false
		}
		if len(p.pending) > 2 && b >= 0x40 && b <= 0x7e {
			p.pending = nil
			switch b {
			case 'A':
				return pickerKey{kind: keyUp}, true
			case 'B':
				return pickerKey{kind: keyDown}, true
			}
			return pickerKey{kind: keyIgnore}, true
		}
		if len(p.pending) < 16 && len(p.pending) > 2 {
			return pickerKey{}, false
		}
		p.pending = nil
		return pickerKey{kind: keyIgnore}, true
	}
	if !utf8.FullRune(p.pending) {
		return pickerKey{}, false
	}
	r, _ := utf8.DecodeRune(p.pending)
	p.pending = nil
	switch r {
	case 3, 4, 26:
		return pickerKey{kind: keyCancel}, true
	case '\r', '\n':
		return pickerKey{kind: keyEnter}, true
	case ' ':
		return pickerKey{kind: keySpace}, true
	case 8, 127:
		return pickerKey{kind: keyBackspace}, true
	case 21:
		return pickerKey{kind: keyReset}, true
	}
	if unicode.IsPrint(r) && !HasControl(string(r)) && r != utf8.RuneError {
		return pickerKey{kind: keyText, text: r}, true
	}
	return pickerKey{kind: keyIgnore}, true
}

func readPickerKey(ctx context.Context, read func(time.Duration) (byte, bool, error), p *keyParser) (pickerKey, error) {
	var last time.Time
	for {
		if err := ctx.Err(); err != nil {
			return pickerKey{}, err
		}
		b, ready, err := read(pickerPoll)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return pickerKey{}, ErrAborted
			}
			return pickerKey{}, fmt.Errorf("reading picker input: %w", err)
		}
		if !ready {
			if len(p.pending) != 0 && time.Since(last) >= 150*time.Millisecond {
				escape := p.pending[0] == 27
				p.pending = nil
				if escape {
					return pickerKey{kind: keyCancel}, nil
				}
			}
			continue
		}
		last = time.Now()
		if k, done := p.feed(b); done {
			return k, nil
		}
	}
}

type pickerState struct {
	q          Question
	multi      bool
	filter     string
	candidates []int
	cursor     int
	selected   []bool
}

func newPickerState(q Question, multi bool) *pickerState {
	p := &pickerState{q: q, multi: multi, candidates: allIndexes(len(q.Options)), selected: make([]bool, len(q.Options))}
	if d, ok := q.defaultIndex(); ok && !multi {
		p.cursor = d
	}
	return p
}

func (p *pickerState) refilter() {
	p.candidates = nil
	for i, o := range p.q.Options {
		f := strings.ToLower(p.filter)
		if strings.Contains(strings.ToLower(SanitizeLine(o.Label)), f) || strings.Contains(strings.ToLower(SanitizeLine(o.Value)), f) {
			p.candidates = append(p.candidates, i)
		}
	}
	p.cursor = 0
}

func (p *pickerState) apply(k pickerKey) (done bool, err error) {
	switch k.kind {
	case keyCancel:
		return false, ErrAborted
	case keyUp:
		if p.cursor > 0 {
			p.cursor--
		}
	case keyDown:
		if p.cursor+1 < len(p.candidates) {
			p.cursor++
		}
	case keyEnter:
		return p.multi || len(p.candidates) > 0, nil
	case keySpace:
		if p.multi {
			if len(p.candidates) > 0 {
				i := p.candidates[p.cursor]
				p.selected[i] = !p.selected[i]
			}
		} else if p.q.Filterable && len(p.filter) < 256 {
			p.filter += " "
			p.refilter()
		}
	case keyText:
		if p.q.Filterable && len(p.filter)+utf8.RuneLen(k.text) <= 256 {
			p.filter += string(k.text)
			p.refilter()
		}
	case keyBackspace:
		if p.filter != "" {
			_, n := utf8.DecodeLastRuneInString(p.filter)
			p.filter = p.filter[:len(p.filter)-n]
			p.refilter()
		}
	case keyReset:
		p.filter = ""
		p.refilter()
	}
	return false, nil
}

func (p *pickerState) result() []int {
	if !p.multi {
		return []int{p.candidates[p.cursor]}
	}
	out := make([]int, 0)
	for i, selected := range p.selected {
		if selected {
			out = append(out, i)
		}
	}
	return out
}

// Limit rune count as well as columns: combining marks occupy no columns,
// so width-based truncation alone would allow unbounded terminal output.
func pickerText(text string) string {
	count := 0
	for i := range text {
		if count == 256 {
			return SanitizeLine(text[:i]) + "…"
		}
		count++
	}
	return SanitizeLine(text)
}

func (p *pickerState) headers(width int) []string {
	legend := "Up/Down move; Enter choose; Esc/Ctrl+C cancel"
	if p.multi {
		legend = "Up/Down move; Space toggle; Enter submit; Esc/Ctrl+C cancel"
	}
	lines := []string{Truncate("? "+pickerText(p.q.Title), width, false)}
	lines = append(lines, wrapLine(legend, width)...)
	if p.q.Filterable {
		lines = append(lines, Truncate("Filter: "+pickerText(p.filter), width, false))
		lines = append(lines, wrapLine("Type to filter; Backspace erase; Ctrl+U reset", width)...)
	}
	count := 0
	for _, v := range p.selected {
		if v {
			count++
		}
	}
	status := fmt.Sprintf("%d matches", len(p.candidates))
	if p.multi {
		status += fmt.Sprintf("; %d selected (including hidden)", count)
	}
	if len(p.candidates) == 0 {
		status = "No matches. Backspace or Ctrl+U resets the filter"
	}
	return append(lines, wrapLine(status, width)...)
}

func (p *pickerState) lines(width, height int) []string {
	lines := p.headers(width)
	rows := min(10, height-len(lines))
	start := max(0, p.cursor-rows+1)
	for pos := start; pos < min(start+rows, len(p.candidates)); pos++ {
		i := p.candidates[pos]
		prefix := "  "
		if pos == p.cursor {
			prefix = "> "
		}
		if p.multi {
			mark := "[ ] "
			if p.selected[i] {
				mark = "[x] "
			}
			prefix += mark
		}
		o := p.q.Options[i]
		line := prefix + pickerText(o.Label)
		if o.Detail != "" {
			line += " - " + pickerText(o.Detail)
		}
		lines = append(lines, line)
	}
	for i, line := range lines {
		lines[i] = Truncate(SanitizeLine(line), width, false)
	}
	return lines
}

// keyboardSelect reports handled=false only before raw mode, when the
// streams/console cannot support a bounded inline picker. Those runs retain
// the numbered interface, including non-file test streams and --plain.
func (t *TTY) keyboardSelect(ctx context.Context, q Question, multi bool) (result []int, handled bool, err error) {
	if !t.mode.Interactive || t.mode.Plain || t.pending != nil || t.r.Buffered() != 0 {
		return nil, false, nil
	}
	in, inOK := t.streams.In.(*os.File)
	out, outOK := t.streams.Err.(*os.File)
	if !inOK || !outOK || in == nil || out == nil || !isTerminal(in.Fd()) || !isTerminal(out.Fd()) {
		return nil, false, nil
	}
	width, height, sizeErr := pickerSize(int(out.Fd())) //nolint:gosec // fd fits in int
	if sizeErr != nil || width < 20 || height < 6 {
		return nil, false, nil
	}
	originalWidth, originalHeight := width, height
	width, height = min(width-1, 100), min(height-1, 14)
	p := newPickerState(q, multi)
	// Keep every key instruction visible and reserve room for an option.
	// Size against both an empty filter result and the largest selection count.
	for i := range p.selected {
		p.selected[i] = true
	}
	if len(p.headers(width))+1 > height {
		return nil, false, nil
	}
	p.candidates = nil
	if len(p.headers(width))+1 > height {
		return nil, false, nil
	}
	p = newPickerState(q, multi)
	if err := ctx.Err(); err != nil {
		return nil, true, err
	}
	restoreOutput, ok := pickerOutput(out)
	if !ok {
		return nil, false, nil
	}
	defer func() { err = errors.Join(err, restoreOutput()) }()
	fd := int(in.Fd()) //nolint:gosec // fd fits in int
	state, rawErr := pickerMakeRaw(fd)
	if rawErr != nil {
		return nil, false, nil
	}
	defer func() { err = errors.Join(err, pickerRestore(fd, state)) }()
	sctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	read := pickerRead(in)
	parser := &keyParser{}
	previous := 0
	for {
		// Resizing invalidates the cursor coordinates of the previous frame.
		// Stop rather than redraw outside the terminal or overwrite other output.
		w, h, err := pickerSize(int(out.Fd())) //nolint:gosec // fd fits in int
		if err != nil {
			return nil, true, fmt.Errorf("reading terminal size: %w", err)
		}
		if w != originalWidth || h != originalHeight {
			return nil, true, errors.New("terminal resized during selection: retry with --plain")
		}
		lines := p.lines(width, height)
		var frame strings.Builder
		if previous > 0 {
			fmt.Fprintf(&frame, "\x1b[%dA", previous)
		}
		n := max(previous, len(lines))
		for i := 0; i < n; i++ {
			frame.WriteString("\r\x1b[2K")
			if i < len(lines) {
				frame.WriteString(lines[i])
			}
			frame.WriteString("\r\n")
		}
		if n > len(lines) {
			fmt.Fprintf(&frame, "\x1b[%dA", n-len(lines))
		}
		if _, err := io.WriteString(out, frame.String()); err != nil {
			return nil, true, fmt.Errorf("rendering picker: %w", err)
		}
		previous = len(lines)
		k, err := readPickerKey(sctx, read, parser)
		if err != nil {
			return nil, true, err
		}
		done, err := p.apply(k)
		if err != nil {
			return nil, true, err
		}
		if done {
			return p.result(), true, nil
		}
	}
}
