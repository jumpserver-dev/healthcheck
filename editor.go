package main

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/term"
)

const (
	keyArrowLeft  = "arrow-left"
	keyArrowRight = "arrow-right"
	keyArrowUp    = "arrow-up"
	keyArrowDown  = "arrow-down"
	keyDelete     = "delete"
	keyHome       = "home"
	keyEnd        = "end"
	keyPageUp     = "page-up"
	keyPageDown   = "page-down"
)

type textEditor struct {
	filename string
	rows     [][]rune
	cx       int
	cy       int
	rowoff   int
	coloff   int
	dirty    bool
	status   string
	reader   *bufio.Reader
}

func editFile(filename string) error {
	if filename == "" {
		return fmt.Errorf("file is required")
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) || !term.IsTerminal(int(os.Stdout.Fd())) {
		return fmt.Errorf("edit requires an interactive terminal")
	}

	oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		return err
	}
	defer term.Restore(int(os.Stdin.Fd()), oldState)

	editor, err := newTextEditor(filename)
	if err != nil {
		return err
	}

	fmt.Print("\x1b[?1049h")
	fmt.Print("\x1b[?25l")
	defer fmt.Print("\x1b[?25h\x1b[?1049l")

	return editor.run()
}

func newTextEditor(filename string) (*textEditor, error) {
	rows := [][]rune{{}}
	data, err := os.ReadFile(filename)
	if err == nil {
		text := strings.ReplaceAll(string(data), "\r\n", "\n")
		text = strings.TrimSuffix(text, "\n")
		parts := strings.Split(text, "\n")
		rows = make([][]rune, len(parts))
		for i, part := range parts {
			rows[i] = []rune(part)
		}
		if len(rows) == 0 {
			rows = [][]rune{{}}
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}

	return &textEditor{
		filename: filename,
		rows:     rows,
		status:   "Ctrl-S save | Ctrl-Q cancel",
		reader:   bufio.NewReader(os.Stdin),
	}, nil
}

func (e *textEditor) run() error {
	for {
		if err := e.refreshScreen(); err != nil {
			return err
		}
		key, err := e.readKey()
		if err != nil {
			return err
		}

		switch key {
		case "\x11", "\x03":
			return nil
		case "\x13":
			if err := e.save(); err != nil {
				e.status = fmt.Sprintf("save failed: %v", err)
				continue
			}
			e.status = "saved"
		case "\r", "\n":
			e.insertNewline()
		case "\x7f", "\b":
			e.deleteBeforeCursor()
		case keyDelete:
			e.deleteAtCursor()
		case keyArrowLeft:
			e.moveCursor(-1, 0)
		case keyArrowRight:
			e.moveCursor(1, 0)
		case keyArrowUp:
			e.moveCursor(0, -1)
		case keyArrowDown:
			e.moveCursor(0, 1)
		case keyHome:
			e.cx = 0
		case keyEnd:
			e.cx = len(e.rows[e.cy])
		case keyPageUp:
			e.movePage(-1)
		case keyPageDown:
			e.movePage(1)
		default:
			if len(key) > 0 {
				r, size := utf8.DecodeRuneInString(key)
				if r != utf8.RuneError || size > 1 {
					e.insertRune(r)
				}
			}
		}
	}
}

func (e *textEditor) refreshScreen() error {
	width, height, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil {
		return err
	}
	if width < 20 || height < 5 {
		return fmt.Errorf("terminal is too small")
	}
	editorRows := height - 2
	lineNumberWidth := e.lineNumberWidth()
	textWidth := width - lineNumberWidth
	if textWidth < 1 {
		return fmt.Errorf("terminal is too narrow")
	}
	e.scroll(textWidth, editorRows)

	var out strings.Builder
	out.WriteString("\x1b[?25l")
	out.WriteString("\x1b[H")
	for y := 0; y < editorRows; y++ {
		fileRow := y + e.rowoff
		out.WriteString("\x1b[K")
		if fileRow >= len(e.rows) {
			e.writeLineNumber(&out, fileRow, lineNumberWidth, false)
			out.WriteByte('~')
		} else {
			e.writeLineNumber(&out, fileRow, lineNumberWidth, true)
			row := e.rows[fileRow]
			if e.coloff < len(row) {
				visible := row[e.coloff:]
				if len(visible) > textWidth {
					visible = visible[:textWidth]
				}
				out.WriteString(string(visible))
			}
		}
		out.WriteString("\r\n")
	}

	e.writeStatusLine(&out, width)
	out.WriteString("\r\n")
	e.writeMessageLine(&out, width)

	cursorY := e.cy - e.rowoff + 1
	cursorX := lineNumberWidth + e.cx - e.coloff + 1
	out.WriteString(fmt.Sprintf("\x1b[%d;%dH", cursorY, cursorX))
	out.WriteString("\x1b[?25h")
	_, err = os.Stdout.WriteString(out.String())
	return err
}

func (e *textEditor) lineNumberWidth() int {
	return len(strconv.Itoa(len(e.rows))) + 2
}

func (e *textEditor) writeLineNumber(out *strings.Builder, row int, width int, exists bool) {
	if exists {
		fmt.Fprintf(out, "\x1b[2m%*d \x1b[m", width-1, row+1)
		return
	}
	out.WriteString(strings.Repeat(" ", width-2))
	out.WriteString("\x1b[2m~ \x1b[m")
}

func (e *textEditor) writeStatusLine(out *strings.Builder, width int) {
	name := e.filename
	if name == "" {
		name = "[No Name]"
	}
	dirty := ""
	if e.dirty {
		dirty = " modified"
	}
	left := fmt.Sprintf(" %.30s - %d lines%s", name, len(e.rows), dirty)
	right := fmt.Sprintf(" %d:%d ", e.cy+1, e.cx+1)
	padding := width - len([]rune(left)) - len([]rune(right))
	if padding < 1 {
		padding = 1
	}
	status := left + strings.Repeat(" ", padding) + right
	runes := []rune(status)
	if len(runes) > width {
		runes = runes[:width]
	}
	out.WriteString("\x1b[7m")
	out.WriteString(string(runes))
	out.WriteString(strings.Repeat(" ", width-len(runes)))
	out.WriteString("\x1b[m")
}

func (e *textEditor) writeMessageLine(out *strings.Builder, width int) {
	out.WriteString("\x1b[K")
	message := []rune(e.status)
	if len(message) > width {
		message = message[:width]
	}
	out.WriteString(string(message))
}

func (e *textEditor) scroll(width int, editorRows int) {
	if e.cy < e.rowoff {
		e.rowoff = e.cy
	}
	if e.cy >= e.rowoff+editorRows {
		e.rowoff = e.cy - editorRows + 1
	}
	if e.cx < e.coloff {
		e.coloff = e.cx
	}
	if e.cx >= e.coloff+width {
		e.coloff = e.cx - width + 1
	}
}

func (e *textEditor) readKey() (string, error) {
	r, _, err := e.reader.ReadRune()
	if err != nil {
		return "", err
	}
	if r != '\x1b' {
		return string(r), nil
	}

	next, _, err := e.reader.ReadRune()
	if err != nil {
		return "\x1b", nil
	}
	if next == 'O' {
		code, _, err := e.reader.ReadRune()
		if err != nil {
			return "\x1b", nil
		}
		switch code {
		case 'A':
			return keyArrowUp, nil
		case 'B':
			return keyArrowDown, nil
		case 'C':
			return keyArrowRight, nil
		case 'D':
			return keyArrowLeft, nil
		case 'H':
			return keyHome, nil
		case 'F':
			return keyEnd, nil
		default:
			return "\x1b", nil
		}
	}
	if next != '[' {
		if err := e.reader.UnreadRune(); err != nil {
			return "", err
		}
		return "\x1b", nil
	}

	var sequence []rune
	for {
		code, _, err := e.reader.ReadRune()
		if err != nil {
			return "\x1b", nil
		}
		sequence = append(sequence, code)
		if code == '~' || unicode.IsLetter(code) {
			break
		}
	}
	return parseCSISequence(sequence), nil
}

func parseCSISequence(sequence []rune) string {
	if len(sequence) == 0 {
		return "\x1b"
	}

	switch sequence[len(sequence)-1] {
	case 'A':
		return keyArrowUp
	case 'B':
		return keyArrowDown
	case 'C':
		return keyArrowRight
	case 'D':
		return keyArrowLeft
	case 'H':
		return keyHome
	case 'F':
		return keyEnd
	case '~':
		switch string(sequence[:len(sequence)-1]) {
		case "1", "7":
			return keyHome
		case "3":
			return keyDelete
		case "4", "8":
			return keyEnd
		case "5":
			return keyPageUp
		case "6":
			return keyPageDown
		}
	}
	return "\x1b"
}

func (e *textEditor) insertRune(r rune) {
	if r < 32 || r == 127 {
		return
	}
	row := e.rows[e.cy]
	row = append(row, 0)
	copy(row[e.cx+1:], row[e.cx:])
	row[e.cx] = r
	e.rows[e.cy] = row
	e.cx++
	e.dirty = true
}

func (e *textEditor) insertNewline() {
	row := e.rows[e.cy]
	left := append([]rune(nil), row[:e.cx]...)
	right := append([]rune(nil), row[e.cx:]...)
	e.rows[e.cy] = left
	e.rows = append(e.rows, nil)
	copy(e.rows[e.cy+2:], e.rows[e.cy+1:])
	e.rows[e.cy+1] = right
	e.cy++
	e.cx = 0
	e.dirty = true
}

func (e *textEditor) deleteBeforeCursor() {
	if e.cy == 0 && e.cx == 0 {
		return
	}
	if e.cx > 0 {
		row := e.rows[e.cy]
		e.rows[e.cy] = append(row[:e.cx-1], row[e.cx:]...)
		e.cx--
		e.dirty = true
		return
	}

	previousLen := len(e.rows[e.cy-1])
	e.rows[e.cy-1] = append(e.rows[e.cy-1], e.rows[e.cy]...)
	e.rows = append(e.rows[:e.cy], e.rows[e.cy+1:]...)
	e.cy--
	e.cx = previousLen
	e.dirty = true
}

func (e *textEditor) deleteAtCursor() {
	row := e.rows[e.cy]
	if e.cx < len(row) {
		e.rows[e.cy] = append(row[:e.cx], row[e.cx+1:]...)
		e.dirty = true
		return
	}
	if e.cy+1 < len(e.rows) {
		e.rows[e.cy] = append(e.rows[e.cy], e.rows[e.cy+1]...)
		e.rows = append(e.rows[:e.cy+1], e.rows[e.cy+2:]...)
		e.dirty = true
	}
}

func (e *textEditor) moveCursor(dx int, dy int) {
	if dy < 0 && e.cy > 0 {
		e.cy--
	}
	if dy > 0 && e.cy+1 < len(e.rows) {
		e.cy++
	}

	if dx < 0 {
		if e.cx > 0 {
			e.cx--
		} else if e.cy > 0 {
			e.cy--
			e.cx = len(e.rows[e.cy])
		}
	}
	if dx > 0 {
		if e.cx < len(e.rows[e.cy]) {
			e.cx++
		} else if e.cy+1 < len(e.rows) {
			e.cy++
			e.cx = 0
		}
	}

	if e.cx > len(e.rows[e.cy]) {
		e.cx = len(e.rows[e.cy])
	}
}

func (e *textEditor) movePage(direction int) {
	_, height, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil {
		return
	}
	rows := height - 2
	if direction < 0 {
		e.cy -= rows
		if e.cy < 0 {
			e.cy = 0
		}
	} else {
		e.cy += rows
		if e.cy >= len(e.rows) {
			e.cy = len(e.rows) - 1
		}
	}
	if e.cx > len(e.rows[e.cy]) {
		e.cx = len(e.rows[e.cy])
	}
}

func (e *textEditor) save() error {
	lines := make([]string, len(e.rows))
	for i, row := range e.rows {
		lines[i] = string(row)
	}
	data := strings.Join(lines, "\n")
	if err := os.WriteFile(e.filename, []byte(data), 0644); err != nil {
		return err
	}
	e.dirty = false
	return nil
}
